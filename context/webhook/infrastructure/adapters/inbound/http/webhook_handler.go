package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/webhook/application"
	"github.com/juantevez/cobros-platform/context/webhook/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// WebhookHandler gestiona el CRUD de endpoints y la consulta de deliveries.
type WebhookHandler struct {
	register     *application.RegisterEndpointUseCase
	deactivate   *application.DeactivateEndpointUseCase
	listEndpoints *application.ListEndpointsUseCase
	listDeliveries *application.ListDeliveriesUseCase
	getDelivery  *application.GetDeliveryUseCase
	retry        *application.RetryDeliveryUseCase
}

func NewWebhookHandler(
	register *application.RegisterEndpointUseCase,
	deactivate *application.DeactivateEndpointUseCase,
	listEndpoints *application.ListEndpointsUseCase,
	listDeliveries *application.ListDeliveriesUseCase,
	getDelivery *application.GetDeliveryUseCase,
	retry *application.RetryDeliveryUseCase,
) *WebhookHandler {
	return &WebhookHandler{
		register:       register,
		deactivate:     deactivate,
		listEndpoints:  listEndpoints,
		listDeliveries: listDeliveries,
		getDelivery:    getDelivery,
		retry:          retry,
	}
}

// ── Endpoints ─────────────────────────────────────────────────────────────────

type registerEndpointReq struct {
	URL         string   `json:"url"         binding:"required"`
	Events      []string `json:"events"      binding:"required,min=1"`
	Description string   `json:"description"`
}

// RegisterEndpoint registra un nuevo webhook endpoint.
//
//	POST /api/v1/webhooks/endpoints
func (h *WebhookHandler) RegisterEndpoint(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req registerEndpointReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	result, err := h.register.Execute(c.Request.Context(), application.RegisterEndpointCmd{
		TenantID:    tenantID,
		URL:         req.URL,
		Events:      req.Events,
		Description: req.Description,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	// El secret solo se muestra aquí, una única vez.
	c.JSON(http.StatusCreated, gin.H{
		"endpoint_id": result.EndpointID,
		"secret":      result.Secret,
		"secret_hint": result.SecretHint,
		"note":        "Store the secret securely. It will not be shown again.",
	})
}

// ListEndpoints lista los endpoints del tenant.
//
//	GET /api/v1/webhooks/endpoints
func (h *WebhookHandler) ListEndpoints(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	views, err := h.listEndpoints.Execute(c.Request.Context(), tenantID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"endpoints": views, "count": len(views)})
}

// DeactivateEndpoint desactiva un endpoint.
//
//	DELETE /api/v1/webhooks/endpoints/:endpointID
func (h *WebhookHandler) DeactivateEndpoint(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	if err := h.deactivate.Execute(c.Request.Context(), application.DeactivateEndpointCmd{
		TenantID:   tenantID,
		EndpointID: c.Param("endpointID"),
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Deliveries ────────────────────────────────────────────────────────────────

// ListDeliveries lista las deliveries del tenant.
//
//	GET /api/v1/webhooks/deliveries?limit=50
func (h *WebhookHandler) ListDeliveries(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	views, err := h.listDeliveries.Execute(c.Request.Context(), application.ListDeliveriesQuery{
		TenantID: tenantID,
		Limit:    limit,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deliveries": views, "count": len(views)})
}

// GetDelivery retorna el detalle de una delivery con todos sus intentos.
//
//	GET /api/v1/webhooks/deliveries/:deliveryID
func (h *WebhookHandler) GetDelivery(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.getDelivery.Execute(c.Request.Context(), application.GetDeliveryQuery{
		TenantID:   tenantID,
		DeliveryID: c.Param("deliveryID"),
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// RetryDelivery fuerza un reintento manual de una delivery fallida.
//
//	POST /api/v1/webhooks/deliveries/:deliveryID/retry
func (h *WebhookHandler) RetryDelivery(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	if err := h.retry.Execute(c.Request.Context(), application.RetryDeliveryCmd{
		TenantID:   tenantID,
		DeliveryID: c.Param("deliveryID"),
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrEndpointNotFound),
		errors.Is(err, domain.ErrDeliveryNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrDeliveryNotRetryable),
		errors.Is(err, domain.ErrEndpointInactive):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrEndpointURLEmpty),
		errors.Is(err, domain.ErrNoEventsSubscribed):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RegisterRoutes registra las rutas de Webhooks en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, h *WebhookHandler) {
	wh := rg.Group("/webhooks")
	{
		wh.POST("/endpoints", h.RegisterEndpoint)
		wh.GET("/endpoints", h.ListEndpoints)
		wh.DELETE("/endpoints/:endpointID", h.DeactivateEndpoint)
		wh.GET("/deliveries", h.ListDeliveries)
		wh.GET("/deliveries/:deliveryID", h.GetDelivery)
		wh.POST("/deliveries/:deliveryID/retry", h.RetryDelivery)
	}
}
