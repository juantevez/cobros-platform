package http

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/notification/application"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// NotificationHandler expone el historial y las preferencias de notificaciones.
type NotificationHandler struct {
	listNotifs    *application.ListNotificationsUseCase
	getPrefs      *application.GetPreferencesUseCase
	updatePref    *application.UpdatePreferenceUseCase
}

func NewNotificationHandler(
	listNotifs *application.ListNotificationsUseCase,
	getPrefs *application.GetPreferencesUseCase,
	updatePref *application.UpdatePreferenceUseCase,
) *NotificationHandler {
	return &NotificationHandler{
		listNotifs: listNotifs,
		getPrefs:   getPrefs,
		updatePref: updatePref,
	}
}

// ListNotifications retorna el historial de notificaciones del tenant.
//
//	GET /api/v1/notifications?limit=50
func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	views, err := h.listNotifs.Execute(c.Request.Context(), tenantID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifications": views, "count": len(views)})
}

// GetPreferences retorna todas las preferencias del tenant.
//
//	GET /api/v1/notifications/preferences
func (h *NotificationHandler) GetPreferences(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	views, err := h.getPrefs.Execute(c.Request.Context(), application.GetPreferencesQuery{
		TenantID: tenantID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"preferences": views})
}

// UpdatePreference activa o desactiva una notificación y configura el email destino.
//
//	PUT /api/v1/notifications/preferences/:eventType
func (h *NotificationHandler) UpdatePreference(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Enabled        bool   `json:"enabled"`
		RecipientEmail string `json:"recipient_email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.updatePref.Execute(c.Request.Context(), application.UpdatePreferenceCmd{
		TenantID:       tenantID,
		EventType:      c.Param("eventType"),
		Enabled:        req.Enabled,
		RecipientEmail: req.RecipientEmail,
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// RegisterRoutes registra las rutas de Notifications en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, h *NotificationHandler) {
	notif := rg.Group("/notifications")
	{
		notif.GET("", h.ListNotifications)
		notif.GET("/preferences", h.GetPreferences)
		notif.PUT("/preferences/:eventType", h.UpdatePreference)
	}
}
