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
//
// La clave de idempotencia es obligatoria (header `Idempotency-Key`, o campo
// `idempotency_key` en el body). Un retry con la misma clave retorna el payout
// existente (200) en lugar de desembolsar dos veces; reutilizar la clave con
// otro monto o moneda devuelve 409.
func (h *PayoutHandler) Initiate(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Amount         int64  `json:"amount"`          // 0 = usar saldo completo
		Currency       string `json:"currency"`        // default ARS
		IdempotencyKey string `json:"idempotency_key"` // opcional si se envía por header
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// El header tiene prioridad; el campo del body es alternativa para clients
	// que no pueden setear headers (mismo patrón que otros endpoints no usan,
	// pero ambos caminos son válidos).
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		key = req.IdempotencyKey
	}

	result, err := h.initiate.Execute(c.Request.Context(), application.InitiatePayoutCmd{
		TenantID:       tenantID,
		Amount:         req.Amount,
		Currency:       req.Currency,
		IdempotencyKey: key,
	})
	switch {
	case errors.Is(err, domain.ErrPayoutRejected):
		// El payout existe y quedó failed (el saldo vuelve al comercio). El
		// detalle del banco no se expone.
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":     domain.ErrPayoutRejected.Error(),
			"payout_id": result.PayoutID,
			"status":    result.Status,
		})
		return
	case errors.Is(err, domain.ErrBankTransferUnknown):
		// El banco no confirmó: el payout queda unknown y lo resuelve el
		// recuperador. 202 con su id para consultarlo.
		c.JSON(http.StatusAccepted, gin.H{
			"payout_id": result.PayoutID,
			"status":    result.Status,
			"detail":    domain.ErrBankTransferUnknown.Error(),
		})
		return
	case err != nil:
		respondDomainError(c, err)
		return
	}
	// Replay idempotente: el recurso ya existía → 200 (no 201).
	if result.Replayed {
		c.JSON(http.StatusOK, result)
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
//	GET /api/v1/payouts?limit=20 (máximo 100)
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
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrIdempotencyKeyRequired),
		errors.Is(err, domain.ErrInvalidID):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrPayoutsNotAllowed):
		c.JSON(http.StatusForbidden, gin.H{"error": domain.ErrPayoutsNotAllowed.Error()})
	case errors.Is(err, domain.ErrIdempotencyKeyConflict),
		errors.Is(err, domain.ErrPayoutAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RouteGuards autoriza cada operación. Se inyectan desde el composition root
// para no acoplar este contexto con Auth; en producción son
// RequirePermission(payouts:create) para desembolsar (retira el saldo del
// comercio: solo admin, y sin API key) y RequirePermission(payments:read)
// para consultar.
type RouteGuards struct {
	Create gin.HandlerFunc // desembolsar
	Read   gin.HandlerFunc // consultar
}

// RegisterRoutes registra las rutas de Payouts en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, handler *PayoutHandler, guards RouteGuards) {
	if guards.Create == nil || guards.Read == nil {
		panic("payout: RegisterRoutes requires Create and Read guards")
	}

	payouts := rg.Group("/payouts")
	{
		payouts.POST("", guards.Create, handler.Initiate)
		payouts.GET("", guards.Read, handler.List)
		payouts.GET("/:payoutID", guards.Read, handler.Get)
	}
}
