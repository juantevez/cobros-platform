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
	listAlerts    *application.ListAlertsUseCase
	getAlert      *application.GetAlertUseCase
	resolveAlert  *application.ResolveAlertUseCase
	addEntry      *application.AddWatchlistEntryUseCase
	removeEntry   *application.RemoveWatchlistEntryUseCase
	listWatchlist *application.ListWatchlistUseCase
}

func NewComplianceHandler(
	listAlerts *application.ListAlertsUseCase,
	getAlert *application.GetAlertUseCase,
	resolveAlert *application.ResolveAlertUseCase,
	addEntry *application.AddWatchlistEntryUseCase,
	removeEntry *application.RemoveWatchlistEntryUseCase,
	listWatchlist *application.ListWatchlistUseCase,
) *ComplianceHandler {
	return &ComplianceHandler{
		listAlerts: listAlerts, getAlert: getAlert, resolveAlert: resolveAlert,
		addEntry: addEntry, removeEntry: removeEntry, listWatchlist: listWatchlist,
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
		switch {
		case errors.Is(err, domain.ErrInvalidTenantID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
		case errors.Is(err, domain.ErrInvalidStatusFilter):
			c.JSON(http.StatusBadRequest, gin.H{"error": domain.ErrInvalidStatusFilter.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
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
		switch {
		case errors.Is(err, domain.ErrAlertNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "alert not found"})
		case errors.Is(err, domain.ErrInvalidTenantID), errors.Is(err, domain.ErrInvalidAlertID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant or alert id"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
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
		ResolvedBy:  postgres.ActorFromContext(c.Request.Context()),
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAlertNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "alert not found"})
		case errors.Is(err, domain.ErrAlertNotOpen):
			c.JSON(http.StatusConflict, gin.H{"error": "alert already resolved"})
		case errors.Is(err, domain.ErrInvalidTenantID), errors.Is(err, domain.ErrInvalidAlertID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant or alert id"})
		case errors.Is(err, domain.ErrInvalidDisposition):
			c.JSON(http.StatusBadRequest, gin.H{"error": domain.ErrInvalidDisposition.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
// Retorna la entrada creada (su id permite darla de baja con DELETE).
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
	view, err := h.addEntry.Execute(c.Request.Context(), application.AddWatchlistEntryCmd{
		FullName: req.FullName, ListType: req.ListType,
		Country: req.Country, Source: req.Source,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidListType), errors.Is(err, domain.ErrInvalidWatchlistName):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}
	c.JSON(http.StatusCreated, view)
}

// RemoveWatchlistEntry da de baja (soft delete) una entrada de la watchlist.
//
//	DELETE /api/v1/compliance/watchlist/:entryID
func (h *ComplianceHandler) RemoveWatchlistEntry(c *gin.Context) {
	err := h.removeEntry.Execute(c.Request.Context(), application.RemoveWatchlistEntryCmd{
		EntryID: c.Param("entryID"),
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrWatchlistEntryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "watchlist entry not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Autorización por ruta ─────────────────────────────────────────────────────

// RouteGuards autoriza cada operación de Compliance. Se inyectan desde el
// composition root (cmd/api) para no acoplar este contexto con Auth; en
// producción son RequireRole(...).
type RouteGuards struct {
	ReadAlerts      gin.HandlerFunc // GET /alerts, /alerts/:id        (admin del tenant)
	ResolveAlert    gin.HandlerFunc // POST /alerts/:id/resolve        (admin del tenant)
	ReadWatchlist   gin.HandlerFunc // GET /watchlist                  (platform_support)
	ManageWatchlist gin.HandlerFunc // POST/DELETE /watchlist          (platform_support)
}

// RegisterRoutes registra las rutas de Compliance en el grupo protegido.
// El grupo debe autenticar (JWT); los guards autorizan por rol. Todos son
// obligatorios: gin saltea los handlers nil, así que un guard olvidado dejaría
// la ruta abierta a cualquier usuario autenticado.
func RegisterRoutes(rg *gin.RouterGroup, h *ComplianceHandler, guards RouteGuards) {
	if guards.ReadAlerts == nil || guards.ResolveAlert == nil ||
		guards.ReadWatchlist == nil || guards.ManageWatchlist == nil {
		panic("compliance: RegisterRoutes requires all RouteGuards (ReadAlerts, ResolveAlert, ReadWatchlist, ManageWatchlist)")
	}

	comp := rg.Group("/compliance")
	{
		comp.GET("/alerts", guards.ReadAlerts, h.ListAlerts)
		comp.GET("/alerts/:alertID", guards.ReadAlerts, h.GetAlert)
		comp.POST("/alerts/:alertID/resolve", guards.ResolveAlert, h.ResolveAlert)
		comp.GET("/watchlist", guards.ReadWatchlist, h.ListWatchlist)
		comp.POST("/watchlist", guards.ManageWatchlist, h.AddWatchlistEntry)
		comp.DELETE("/watchlist/:entryID", guards.ManageWatchlist, h.RemoveWatchlistEntry)
	}
}
