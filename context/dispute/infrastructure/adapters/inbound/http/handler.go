package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type DisputeHandler struct {
	open    *application.OpenDisputeUseCase
	contest *application.ContestDisputeUseCase
	accept  *application.AcceptDisputeUseCase
	resolve *application.ResolveDisputeUseCase
	get     *application.GetDisputeUseCase
	list    *application.ListDisputesUseCase
}

func NewDisputeHandler(
	open *application.OpenDisputeUseCase,
	contest *application.ContestDisputeUseCase,
	accept *application.AcceptDisputeUseCase,
	resolve *application.ResolveDisputeUseCase,
	get *application.GetDisputeUseCase,
	list *application.ListDisputesUseCase,
) *DisputeHandler {
	return &DisputeHandler{open: open, contest: contest, accept: accept,
		resolve: resolve, get: get, list: list}
}

// Open registra una nueva disputa notificada por el banco.
//
//	POST /api/v1/disputes
//
// Lo ejecuta el operador de plataforma: el tenant de la disputa es el del
// dueño del pago, no el de su JWT.
func (h *DisputeHandler) Open(c *gin.Context) {
	var req struct {
		PaymentID    string `json:"payment_id"    binding:"required"`
		PSPReference string `json:"psp_reference"`
		Amount       int64  `json:"amount"        binding:"required,min=1"`
		Currency     string `json:"currency"      binding:"required"`
		Reason       string `json:"reason"        binding:"required"`
		Deadline     string `json:"deadline"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	cmd := application.OpenDisputeCmd{
		PaymentID:    req.PaymentID,
		PSPReference: req.PSPReference,
		Amount:       req.Amount,
		Currency:     req.Currency,
		Reason:       req.Reason,
	}
	if req.Deadline != "" {
		if t, err := time.Parse(time.RFC3339, req.Deadline); err == nil {
			cmd.Deadline = t.UTC()
		}
	}

	result, err := h.open.Execute(c.Request.Context(), cmd)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"dispute_id": result.DisputeID})
}

// List lista las disputas del tenant.
//
//	GET /api/v1/disputes?status=open&limit=50
func (h *DisputeHandler) List(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	views, err := h.list.Execute(c.Request.Context(), application.ListDisputesQuery{
		TenantID:     tenantID,
		StatusFilter: c.Query("status"),
		Limit:        limit,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"disputes": views, "count": len(views)})
}

// Get retorna el detalle de una disputa con su evidencia.
//
//	GET /api/v1/disputes/:disputeID
func (h *DisputeHandler) Get(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.get.Execute(c.Request.Context(), application.GetDisputeQuery{
		TenantID:  tenantID,
		DisputeID: c.Param("disputeID"),
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// Contest envía evidencia para contestar la disputa.
//
//	POST /api/v1/disputes/:disputeID/contest
func (h *DisputeHandler) Contest(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Evidence []struct {
			EvidenceType string `json:"evidence_type" binding:"required"`
			Reference    string `json:"reference"     binding:"required"`
			Description  string `json:"description"`
		} `json:"evidence" binding:"required,min=1"`
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	evidence := make([]application.EvidenceInput, len(req.Evidence))
	for i, e := range req.Evidence {
		evidence[i] = application.EvidenceInput{
			EvidenceType: e.EvidenceType,
			Reference:    e.Reference,
			Description:  e.Description,
		}
	}

	if err := h.contest.Execute(c.Request.Context(), application.ContestDisputeCmd{
		TenantID:  tenantID,
		DisputeID: c.Param("disputeID"),
		Evidence:  evidence,
		Note:      req.Note,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Accept acepta la disputa voluntariamente.
//
//	POST /api/v1/disputes/:disputeID/accept
func (h *DisputeHandler) Accept(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req struct {
		Note string `json:"note"`
	}
	// El body es opcional (solo la nota); ignoramos el error de binding a propósito.
	_ = c.ShouldBindJSON(&req)

	if err := h.accept.Execute(c.Request.Context(), application.AcceptDisputeCmd{
		TenantID:  tenantID,
		DisputeID: c.Param("disputeID"),
		Note:      req.Note,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Resolve registra el resultado final del banco. Solo platform_support.
//
//	POST /api/v1/disputes/:disputeID/resolve
func (h *DisputeHandler) Resolve(c *gin.Context) {
	var req struct {
		Outcome string `json:"outcome" binding:"required"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.resolve.Execute(c.Request.Context(), application.ResolveDisputeCmd{
		DisputeID: c.Param("disputeID"),
		Outcome:   req.Outcome,
		Note:      req.Note,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrDisputeNotFound),
		errors.Is(err, domain.ErrPaymentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrDuplicateDispute),
		errors.Is(err, domain.ErrDisputeAlreadyClosed),
		errors.Is(err, domain.ErrOptimisticLock):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrDisputeExpired),
		errors.Is(err, domain.ErrEvidenceRequired):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidDisputeReason),
		errors.Is(err, domain.ErrInvalidResolutionOutcome),
		errors.Is(err, domain.ErrPaymentNotCaptured),
		errors.Is(err, domain.ErrAmountExceedsPayment),
		errors.Is(err, domain.ErrCurrencyMismatch),
		errors.Is(err, domain.ErrInvalidDeadline),
		errors.Is(err, domain.ErrInvalidEvidenceType):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RouteGuards autoriza cada operación. Las rutas de Operator corren además con
// platformOperatorScope (RLS). Se inyectan desde el composition root
// para no acoplar este contexto con Auth:
//   - Operator: abrir la disputa y registrar el fallo del banco son acciones
//     del operador de plataforma (RequireRole(platform_support)). Abrir
//     congela fondos del comercio.
//   - Manage: contestar o aceptar (RequirePermission(disputes:manage)).
//     Aceptar entrega el monto disputado.
//   - Read: consultar (RequirePermission(payments:read)).
type RouteGuards struct {
	Operator gin.HandlerFunc
	Manage   gin.HandlerFunc
	Read     gin.HandlerFunc
}

func RegisterRoutes(rg *gin.RouterGroup, h *DisputeHandler, guards RouteGuards) {
	if guards.Operator == nil || guards.Manage == nil || guards.Read == nil {
		panic("dispute: RegisterRoutes requires Operator, Manage and Read guards")
	}

	d := rg.Group("/disputes")
	{
		d.POST("", guards.Operator, platformOperatorScope, h.Open)
		d.GET("", guards.Read, h.List)
		d.GET("/:disputeID", guards.Read, h.Get)
		d.POST("/:disputeID/contest", guards.Manage, h.Contest)
		d.POST("/:disputeID/accept", guards.Manage, h.Accept)
		d.POST("/:disputeID/resolve", guards.Operator, platformOperatorScope, h.Resolve)
	}
}

// platformOperatorScope marca el request como acción de operador para RLS:
// la disputa (y el pago) son de otro tenant, así que la lectura pasa por la
// política de operador, nunca por el tenant del JWT. Debe ir siempre DESPUÉS
// del guard de rol.
func platformOperatorScope(c *gin.Context) {
	c.Request = c.Request.WithContext(postgres.WithPlatformOperator(c.Request.Context()))
	c.Next()
}
