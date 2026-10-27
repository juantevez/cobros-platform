package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/billing/application"
	"github.com/juantevez/cobros-platform/context/billing/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// BillingHandler expone la gestión de planes y el plan activo del tenant.
type BillingHandler struct {
	createPlan       *application.CreatePlanUseCase
	updatePlan       *application.UpdatePlanUseCase
	deactivatePlan   *application.DeactivatePlanUseCase
	assignPlan       *application.AssignPlanUseCase
	getPlan          *application.GetPlanUseCase
	listPlans        *application.ListPlansPageUseCase
	getTenantPlan    *application.GetTenantPlanUseCase
	listTenantPlans  *application.ListTenantPlansUseCase
}

func NewBillingHandler(
	createPlan *application.CreatePlanUseCase,
	updatePlan *application.UpdatePlanUseCase,
	deactivatePlan *application.DeactivatePlanUseCase,
	assignPlan *application.AssignPlanUseCase,
	getPlan *application.GetPlanUseCase,
	listPlans *application.ListPlansPageUseCase,
	getTenantPlan *application.GetTenantPlanUseCase,
	listTenantPlans *application.ListTenantPlansUseCase,
) *BillingHandler {
	return &BillingHandler{
		createPlan:      createPlan,
		updatePlan:      updatePlan,
		deactivatePlan:  deactivatePlan,
		assignPlan:      assignPlan,
		getPlan:         getPlan,
		listPlans:       listPlans,
		getTenantPlan:   getTenantPlan,
		listTenantPlans: listTenantPlans,
	}
}

// ── Planes (operador) ─────────────────────────────────────────────────────────

type createPlanReq struct {
	Name            string          `json:"name"      binding:"required"`
	Description     string          `json:"description"`
	BaseRateBps     int64           `json:"base_rate_bps"`     // 0 es válido (plan sin comisión proporcional)
	BaseFixedAmount int64           `json:"base_fixed_amount"` // ≥ 0; la validación es del dominio
	MonthlyFee      int64           `json:"monthly_fee"`       // ≥ 0; la validación es del dominio
	Currency        string          `json:"currency"  binding:"required"`
	MethodRates     []methodRateReq `json:"method_rates"`
}

type methodRateReq struct {
	Method      string `json:"method"       binding:"required"`
	RateBps     int64  `json:"rate_bps"`     // 0 es válido
	FixedAmount int64  `json:"fixed_amount"` // ≥ 0; la validación es del dominio
}

// CreatePlan crea un nuevo plan de tarifas.
//
//	POST /api/v1/billing/plans
func (h *BillingHandler) CreatePlan(c *gin.Context) {
	var req createPlanReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	methodRates := make([]application.MethodRateInput, len(req.MethodRates))
	for i, mr := range req.MethodRates {
		methodRates[i] = application.MethodRateInput{
			Method:      mr.Method,
			RateBps:     mr.RateBps,
			FixedAmount: mr.FixedAmount,
		}
	}

	result, err := h.createPlan.Execute(c.Request.Context(), application.CreatePlanCmd{
		Name:            req.Name,
		Description:     req.Description,
		BaseRateBps:     req.BaseRateBps,
		BaseFixedAmount: req.BaseFixedAmount,
		MonthlyFee:      req.MonthlyFee,
		Currency:        req.Currency,
		MethodRates:     methodRates,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"plan_id": result.PlanID})
}

// ListPlans lista los planes activos del catálogo.
//
//	GET /api/v1/billing/plans
func (h *BillingHandler) ListPlans(c *gin.Context) {
	views, err := h.listPlans.Execute(c.Request.Context())
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"plans": views, "count": len(views)})
}

// GetPlan consulta un plan por ID.
//
//	GET /api/v1/billing/plans/:planID
func (h *BillingHandler) GetPlan(c *gin.Context) {
	view, err := h.getPlan.Execute(c.Request.Context(),
		application.GetPlanQuery{PlanID: c.Param("planID")})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ── Asignación por tenant (operador) ──────────────────────────────────────────

type assignPlanReq struct {
	PlanID            string `json:"plan_id"             binding:"required"`
	CustomRateBps     *int64 `json:"custom_rate_bps"`     // nil = sin override
	CustomFixedAmount *int64 `json:"custom_fixed_amount"` // nil = sin override
}

// AssignPlan asigna un plan a un tenant específico.
//
//	POST /api/v1/billing/tenants/:tenantID/plan
func (h *BillingHandler) AssignPlan(c *gin.Context) {
	var req assignPlanReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// -1 indica "sin override" al dominio.
	customRate := int64(-1)
	if req.CustomRateBps != nil {
		customRate = *req.CustomRateBps
	}
	customFixed := int64(-1)
	if req.CustomFixedAmount != nil {
		customFixed = *req.CustomFixedAmount
	}

	result, err := h.assignPlan.Execute(c.Request.Context(), application.AssignPlanCmd{
		TenantID:          c.Param("tenantID"),
		PlanID:            req.PlanID,
		CustomRateBps:     customRate,
		CustomFixedAmount: customFixed,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ── Plan del tenant autenticado ───────────────────────────────────────────────

// GetMyPlan retorna el plan activo del tenant autenticado.
//
//	GET /api/v1/billing/my-plan
func (h *BillingHandler) GetMyPlan(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.getTenantPlan.Execute(c.Request.Context(),
		application.GetTenantPlanQuery{TenantID: tenantID})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func respondDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrPlanNotFound),
		errors.Is(err, domain.ErrTenantPlanNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrPlanInactive):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidRateBps),
		errors.Is(err, domain.ErrInvalidFixedAmount),
		errors.Is(err, domain.ErrInvalidMonthlyFee),
		errors.Is(err, domain.ErrPlanNameEmpty),
		errors.Is(err, domain.ErrInvalidPaymentMethod),
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrCurrencyMismatch):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// ── Autorización por ruta ─────────────────────────────────────────────────────

// RouteGuards autoriza cada operación de Billing. Se inyectan desde el
// composition root (cmd/api) para no acoplar este contexto con Auth; en
// producción son RequireRole(...). Es obligatorio: RegisterRoutes entra en
// pánico si falta alguno, igual que Payment.
type RouteGuards struct {
	ManagePlans gin.HandlerFunc // POST /plans            (platform_support)
	ReadPlans   gin.HandlerFunc // GET  /plans, /plans/:id (cualquiera autenticado)
	AssignPlan  gin.HandlerFunc // POST /tenants/:id/plan  (platform_support)
	ReadMyPlan  gin.HandlerFunc // GET  /my-plan           (admin del tenant)
}

// RegisterRoutes registra las rutas de Billing en el grupo protegido.
// El grupo debe autenticar (JWT); los guards autorizan por rol/scope.
func RegisterRoutes(rg *gin.RouterGroup, h *BillingHandler, guards RouteGuards) {
	if guards.ManagePlans == nil || guards.AssignPlan == nil || guards.ReadMyPlan == nil {
		panic("billing: RegisterRoutes requires ManagePlans, AssignPlan and ReadMyPlan guards")
	}

	billing := rg.Group("/billing")
	{
		// Gestión de planes (platform_support)
		billing.POST("/plans", guards.ManagePlans, h.CreatePlan)
		billing.GET("/plans", guards.ReadPlans, h.ListPlans)
		billing.GET("/plans/:planID", guards.ReadPlans, h.GetPlan)
		billing.POST("/tenants/:tenantID/plan", guards.AssignPlan, h.AssignPlan)

		// Plan del tenant autenticado (admin)
		billing.GET("/my-plan", guards.ReadMyPlan, h.GetMyPlan)
	}
}
