package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// ComplianceHandler expone la revisión de alertas AML y la gestión de la
// watchlist. Todo es de uso interno de la plataforma: el equipo de
// cumplimiento revisa alertas de cualquier comercio y el comercio nunca ve ni
// dispone sus propias alertas (anti tipping-off).
type ComplianceHandler struct {
	listAlerts     *application.ListAlertsUseCase
	getAlert       *application.GetAlertUseCase
	resolveAlert   *application.ResolveAlertUseCase
	approveAlert   *application.ApproveAlertProposalUseCase
	rejectProposal *application.RejectAlertProposalUseCase
	addEntry       *application.AddWatchlistEntryUseCase
	removeEntry    *application.RemoveWatchlistEntryUseCase
	listWatchlist  *application.ListWatchlistUseCase
}

// AlertUseCases agrupa los casos de uso de consulta y revisión de alertas.
type AlertUseCases struct {
	List           *application.ListAlertsUseCase
	Get            *application.GetAlertUseCase
	Resolve        *application.ResolveAlertUseCase
	Approve        *application.ApproveAlertProposalUseCase
	RejectProposal *application.RejectAlertProposalUseCase
}

// WatchlistUseCases agrupa los casos de uso de gestión de la watchlist.
type WatchlistUseCases struct {
	Add    *application.AddWatchlistEntryUseCase
	Remove *application.RemoveWatchlistEntryUseCase
	List   *application.ListWatchlistUseCase
}

func NewComplianceHandler(alerts AlertUseCases, watchlist WatchlistUseCases) *ComplianceHandler {
	return &ComplianceHandler{
		listAlerts: alerts.List, getAlert: alerts.Get, resolveAlert: alerts.Resolve,
		approveAlert: alerts.Approve, rejectProposal: alerts.RejectProposal,
		addEntry: watchlist.Add, removeEntry: watchlist.Remove, listWatchlist: watchlist.List,
	}
}

// ListAlerts lista alertas de toda la plataforma (recientes primero).
//
//	GET /api/v1/compliance/alerts?tenant_id=&status=pending_second_approval&limit=50
func (h *ComplianceHandler) ListAlerts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	views, err := h.listAlerts.Execute(c.Request.Context(), application.ListAlertsQuery{
		TenantID:     c.Query("tenant_id"),
		StatusFilter: c.Query("status"),
		Limit:        limit,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidTenantID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id"})
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
	view, err := h.getAlert.Execute(c.Request.Context(), application.GetAlertQuery{
		AlertID: c.Param("alertID"),
	})
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ResolveAlert es la disposición del primer analista (cleared|confirmed).
// En alertas four-eyes queda pending_second_approval; si no, se cierra. En
// ambos casos responde 200 con la alerta resultante.
//
//	POST /api/v1/compliance/alerts/:alertID/resolve
func (h *ComplianceHandler) ResolveAlert(c *gin.Context) {
	var req struct {
		Disposition string `json:"disposition"`
		Note        string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	err := h.resolveAlert.Execute(c.Request.Context(), application.ResolveAlertCmd{
		AlertID:     c.Param("alertID"),
		Disposition: req.Disposition,
		Note:        req.Note,
		Analyst:     postgres.ActorFromContext(c.Request.Context()),
	})
	if err != nil {
		writeReviewError(c, err)
		return
	}
	h.respondWithAlert(c)
}

// ApproveAlert es la segunda aprobación four-eyes (otro analista).
//
//	POST /api/v1/compliance/alerts/:alertID/approve
func (h *ComplianceHandler) ApproveAlert(c *gin.Context) {
	h.reviewProposal(c, h.approveAlert.Execute)
}

// RejectAlertProposal rechaza la propuesta; la alerta vuelve a open.
//
//	POST /api/v1/compliance/alerts/:alertID/reject
func (h *ComplianceHandler) RejectAlertProposal(c *gin.Context) {
	h.reviewProposal(c, h.rejectProposal.Execute)
}

func (h *ComplianceHandler) reviewProposal(c *gin.Context, exec func(ctx context.Context, cmd application.ReviewProposalCmd) error) {
	var req struct {
		Note string `json:"note"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
	}
	err := exec(c.Request.Context(), application.ReviewProposalCmd{
		AlertID: c.Param("alertID"),
		Note:    req.Note,
		Analyst: postgres.ActorFromContext(c.Request.Context()),
	})
	if err != nil {
		writeReviewError(c, err)
		return
	}
	h.respondWithAlert(c)
}

// respondWithAlert devuelve la alerta tras la transición, para que el
// analista vea si quedó cerrada o pendiente de segunda aprobación.
func (h *ComplianceHandler) respondWithAlert(c *gin.Context) {
	view, err := h.getAlert.Execute(c.Request.Context(), application.GetAlertQuery{AlertID: c.Param("alertID")})
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func writeReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrAlertNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "alert not found"})
	case errors.Is(err, domain.ErrAlertNotOpen):
		c.JSON(http.StatusConflict, gin.H{"error": "alert already resolved"})
	case errors.Is(err, domain.ErrAlertPendingApproval), errors.Is(err, domain.ErrAlertNotPendingApproval):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrSameAnalyst):
		c.JSON(http.StatusForbidden, gin.H{"error": domain.ErrSameAnalyst.Error()})
	case errors.Is(err, domain.ErrInvalidAlertID):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid alert id"})
	case errors.Is(err, domain.ErrInvalidDisposition):
		c.JSON(http.StatusBadRequest, gin.H{"error": domain.ErrInvalidDisposition.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
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
// producción son RequireRole(...) con roles de plataforma.
type RouteGuards struct {
	ReadAlerts      gin.HandlerFunc // GET /alerts, /alerts/:id              (compliance_officer, platform_support)
	ReviewAlerts    gin.HandlerFunc // POST /alerts/:id/{resolve,approve,reject} (compliance_officer)
	ReadWatchlist   gin.HandlerFunc // GET /watchlist                        (compliance_officer, platform_support)
	ManageWatchlist gin.HandlerFunc // POST/DELETE /watchlist                (compliance_officer, platform_support)
}

// platformOperator marca la request como acción de operador de plataforma:
// habilita la política RLS cross-tenant de aml_alerts. Va siempre DESPUÉS del
// guard de rol, para que nunca se fije sin autorización.
func platformOperator(c *gin.Context) {
	c.Request = c.Request.WithContext(postgres.WithPlatformOperator(c.Request.Context()))
	c.Next()
}

// RegisterRoutes registra las rutas de Compliance en el grupo protegido.
// El grupo debe autenticar (JWT); los guards autorizan por rol. Todos son
// obligatorios: gin saltea los handlers nil, así que un guard olvidado dejaría
// la ruta abierta a cualquier usuario autenticado.
func RegisterRoutes(rg *gin.RouterGroup, h *ComplianceHandler, guards RouteGuards) {
	if guards.ReadAlerts == nil || guards.ReviewAlerts == nil ||
		guards.ReadWatchlist == nil || guards.ManageWatchlist == nil {
		panic("compliance: RegisterRoutes requires all RouteGuards (ReadAlerts, ReviewAlerts, ReadWatchlist, ManageWatchlist)")
	}

	comp := rg.Group("/compliance")
	{
		comp.GET("/alerts", guards.ReadAlerts, platformOperator, h.ListAlerts)
		comp.GET("/alerts/:alertID", guards.ReadAlerts, platformOperator, h.GetAlert)
		comp.POST("/alerts/:alertID/resolve", guards.ReviewAlerts, platformOperator, h.ResolveAlert)
		comp.POST("/alerts/:alertID/approve", guards.ReviewAlerts, platformOperator, h.ApproveAlert)
		comp.POST("/alerts/:alertID/reject", guards.ReviewAlerts, platformOperator, h.RejectAlertProposal)
		comp.GET("/watchlist", guards.ReadWatchlist, h.ListWatchlist)
		comp.POST("/watchlist", guards.ManageWatchlist, h.AddWatchlistEntry)
		comp.DELETE("/watchlist/:entryID", guards.ManageWatchlist, h.RemoveWatchlistEntry)
	}
}
