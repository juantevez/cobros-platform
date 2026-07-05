package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// ComplianceHandler expone las alertas de AML y la gestión de la watchlist.
type ComplianceHandler struct {
	listAlerts   *application.ListAlertsUseCase
	getAlert     *application.GetAlertUseCase
	resolveAlert *application.ResolveAlertUseCase
	addEntry     *application.AddWatchlistEntryUseCase
	listWatchlist *application.ListWatchlistUseCase
}

func NewComplianceHandler(
	listAlerts *application.ListAlertsUseCase,
	getAlert *application.GetAlertUseCase,
	resolveAlert *application.ResolveAlertUseCase,
	addEntry *application.AddWatchlistEntryUseCase,
	listWatchlist *application.ListWatchlistUseCase,
) *ComplianceHandler {
	return &ComplianceHandler{
		listAlerts: listAlerts, getAlert: getAlert, resolveAlert: resolveAlert,
		addEntry: addEntry, listWatchlist: listWatchlist,
	}
}

// ListAlerts retorna las alertas del tenant.
//
//	GET /api/v1/compliance/alerts?status=open&limit=50
func (h *ComplianceHandler) ListAlerts(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	views, err := h.listAlerts.Execute(c.Request.Context(), application.ListAlertsQuery{
		TenantID:     tenantID,
		StatusFilter: c.Query("status"),
		Limit:        limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": views, "count": len(views)})
}

// GetAlert retorna una alerta puntual.
//
//	GET /api/v1/compliance/alerts/:alertID
func (h *ComplianceHandler) GetAlert(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.getAlert.Execute(c.Request.Context(), application.GetAlertQuery{
		TenantID: tenantID,
		AlertID:  c.Param("alertID"),
	})
	if err != nil {
		if errors.Is(err, domain.ErrAlertNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "alert not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

// ResolveAlert dispone una alerta (cleared|confirmed).
//
//	POST /api/v1/compliance/alerts/:alertID/resolve
func (h *ComplianceHandler) ResolveAlert(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Disposition string `json:"disposition"`
		Note        string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	err := h.resolveAlert.Execute(c.Request.Context(), application.ResolveAlertCmd{
		TenantID:    tenantID,
		AlertID:     c.Param("alertID"),
		Disposition: req.Disposition,
		Note:        req.Note,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAlertNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "alert not found"})
		case errors.Is(err, domain.ErrAlertNotOpen):
			c.JSON(http.StatusConflict, gin.H{"error": "alert already resolved"})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// ListWatchlist retorna la lista de vigilancia global.
//
//	GET /api/v1/compliance/watchlist?limit=100
func (h *ComplianceHandler) ListWatchlist(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	views, err := h.listWatchlist.Execute(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"watchlist": views, "count": len(views)})
}

// AddWatchlistEntry agrega una entrada a la watchlist global.
//
//	POST /api/v1/compliance/watchlist
func (h *ComplianceHandler) AddWatchlistEntry(c *gin.Context) {
	var req struct {
		FullName string `json:"full_name"`
		ListType string `json:"list_type"`
		Country  string `json:"country"`
		Source   string `json:"source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.FullName == "" || req.ListType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "full_name and list_type are required"})
		return
	}
	if err := h.addEntry.Execute(c.Request.Context(), application.AddWatchlistEntryCmd{
		FullName: req.FullName, ListType: req.ListType,
		Country: req.Country, Source: req.Source,
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

// RegisterRoutes registra las rutas de Compliance en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, h *ComplianceHandler) {
	comp := rg.Group("/compliance")
	{
		comp.GET("/alerts", h.ListAlerts)
		comp.GET("/alerts/:alertID", h.GetAlert)
		comp.POST("/alerts/:alertID/resolve", h.ResolveAlert)
		comp.GET("/watchlist", h.ListWatchlist)
		comp.POST("/watchlist", h.AddWatchlistEntry)
	}
}
