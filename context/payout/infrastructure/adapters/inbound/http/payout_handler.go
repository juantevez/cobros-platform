package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/payout/application"
	"github.com/juantevez/cobros-platform/context/payout/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type PayoutHandler struct {
	initiate  *application.InitiatePayoutUseCase
	getOne    *application.GetPayoutUseCase
	listAll   *application.ListPayoutsUseCase
}

func NewPayoutHandler(
	initiate *application.InitiatePayoutUseCase,
	getOne *application.GetPayoutUseCase,
	listAll *application.ListPayoutsUseCase,
) *PayoutHandler {
	return &PayoutHandler{initiate: initiate, getOne: getOne, listAll: listAll}
}

// Initiate solicita un desembolso.
//
//	POST /api/v1/payouts
func (h *PayoutHandler) Initiate(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Amount   int64  `json:"amount"`   // 0 = usar saldo completo
		Currency string `json:"currency"` // default ARS
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	result, err := h.initiate.Execute(c.Request.Context(), application.InitiatePayoutCmd{
		TenantID: tenantID,
		Amount:   req.Amount,
		Currency: req.Currency,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// Get consulta el estado de un payout.
//
//	GET /api/v1/payouts/:payoutID
func (h *PayoutHandler) Get(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.getOne.Execute(c.Request.Context(), application.GetPayoutQuery{
		TenantID: tenantID,
		PayoutID: c.Param("payoutID"),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// List lista los payouts del tenant autenticado.
//
//	GET /api/v1/payouts?limit=20
func (h *PayoutHandler) List(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	views, err := h.listAll.Execute(c.Request.Context(), application.ListPayoutsQuery{
		TenantID: tenantID,
		Limit:    limit,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"payouts": views, "count": len(views)})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func respondDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrPayoutNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInsufficientBalance),
		errors.Is(err, domain.ErrNoBankAccount),
		errors.Is(err, domain.ErrInvalidTransition):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidCurrency):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RegisterRoutes registra las rutas de Payouts en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, handler *PayoutHandler) {
	payouts := rg.Group("/payouts")
	{
		payouts.POST("", handler.Initiate)
		payouts.GET("", handler.List)
		payouts.GET("/:payoutID", handler.Get)
	}
}
