package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// PaymentHandler gestiona los endpoints de procesamiento de pagos.
type PaymentHandler struct {
	process *application.ProcessPaymentUseCase
	refund  *application.RefundPaymentUseCase
	get     *application.GetPaymentUseCase
}

func NewPaymentHandler(
	process *application.ProcessPaymentUseCase,
	refund *application.RefundPaymentUseCase,
	get *application.GetPaymentUseCase,
) *PaymentHandler {
	return &PaymentHandler{process: process, refund: refund, get: get}
}

// ── Process ───────────────────────────────────────────────────────────────────

type processPaymentReq struct {
	IdempotencyKey string            `json:"idempotency_key" binding:"required"`
	Amount         int64             `json:"amount"          binding:"required,min=1"`
	Currency       string            `json:"currency"        binding:"required"`
	PaymentMethod  string            `json:"payment_method"  binding:"required"`
	PaymentToken   string            `json:"payment_token"   binding:"required"`
	Description    string            `json:"description"`
	PayerName      string            `json:"payer_name"`
	PayerEmail     string            `json:"payer_email"`
	PayerDocType   string            `json:"payer_doc_type"`
	PayerDocNum    string            `json:"payer_doc_num"`
	Metadata       map[string]string `json:"metadata"`
}

// Process captura un pago.
//
//	POST /api/v1/payments
func (h *PaymentHandler) Process(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req processPaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	result, err := h.process.Execute(c.Request.Context(), application.ProcessPaymentCmd{
		TenantID:       tenantID,
		IdempotencyKey: req.IdempotencyKey,
		Amount:         req.Amount,
		Currency:       req.Currency,
		PaymentMethod:  req.PaymentMethod,
		PaymentToken:   req.PaymentToken,
		Description:    req.Description,
		PayerName:      req.PayerName,
		PayerEmail:     req.PayerEmail,
		PayerDocType:   req.PayerDocType,
		PayerDocNum:    req.PayerDocNum,
		Metadata:       req.Metadata,
	})
	if errors.Is(err, domain.ErrPaymentDeclined) {
		// El pago existe y quedó failed: se informa su id y estado para que el
		// cliente pueda consultarlo. El detalle del PSP no se expone.
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":      domain.ErrPaymentDeclined.Error(),
			"payment_id": result.PaymentID,
			"status":     result.Status,
		})
		return
	}
	if errors.Is(err, domain.ErrPaymentPending) {
		// El PSP no confirmó (timeout, red): el pago sigue en processing y se
		// resuelve después. 202 con su id para consultarlo.
		c.JSON(http.StatusAccepted, gin.H{
			"payment_id": result.PaymentID,
			"status":     result.Status,
			"detail":     domain.ErrPaymentPending.Error(),
		})
		return
	}
	if err != nil {
		respondDomainError(c, err)
		return
	}

	status := http.StatusCreated
	if result.WasExisting {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// ── Get ───────────────────────────────────────────────────────────────────────

// Get consulta el estado de un pago.
//
//	GET /api/v1/payments/:paymentID
func (h *PaymentHandler) Get(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	view, err := h.get.Execute(c.Request.Context(), application.GetPaymentQuery{
		TenantID:  tenantID,
		PaymentID: c.Param("paymentID"),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ── Refund ────────────────────────────────────────────────────────────────────

// Refund inicia el reembolso de un pago capturado.
//
//	POST /api/v1/payments/:paymentID/refund
func (h *PaymentHandler) Refund(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	result, err := h.refund.Execute(c.Request.Context(), application.RefundPaymentCmd{
		TenantID:  tenantID,
		PaymentID: c.Param("paymentID"),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func respondDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrPaymentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrPaymentAlreadyExists),
		errors.Is(err, domain.ErrIdempotencyKeyConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrTenantNotActive):
		// Mensaje fijo: el detalle (pending/suspended) no se expone al caller.
		c.JSON(http.StatusForbidden, gin.H{"error": domain.ErrTenantNotActive.Error()})
	case errors.Is(err, domain.ErrPaymentDeclined):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": domain.ErrPaymentDeclined.Error()})
	case errors.Is(err, domain.ErrRiskRejected):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotCaptured),
		errors.Is(err, domain.ErrAlreadyCaptured),
		errors.Is(err, domain.ErrInvalidTransition):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrInvalidMethod):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RouteGuards autoriza cada operación. Se inyectan desde el composition root
// para no acoplar este contexto con Auth; en producción son
// RequirePermission(acción del rol, scopes de API key).
type RouteGuards struct {
	Create gin.HandlerFunc // cobrar
	Read   gin.HandlerFunc // consultar
	Refund gin.HandlerFunc // reembolsar (devuelve dinero)
}

// RegisterRoutes registra las rutas de Payment. El grupo debe autenticar
// (JWT o API key) y poblar el tenant en el contexto; los guards autorizan.
func RegisterRoutes(rg *gin.RouterGroup, handler *PaymentHandler, guards RouteGuards) {
	if guards.Create == nil || guards.Read == nil || guards.Refund == nil {
		panic("payment: RegisterRoutes requires Create, Read and Refund guards")
	}

	payments := rg.Group("/payments")
	{
		payments.POST("", guards.Create, handler.Process)
		payments.GET("/:paymentID", guards.Read, handler.Get)
		payments.POST("/:paymentID/refund", guards.Refund, handler.Refund)
	}
}
