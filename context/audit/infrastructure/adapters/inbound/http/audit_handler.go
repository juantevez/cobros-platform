package http

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/audit/application"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// AuditHandler expone el log de auditoría via HTTP.
type AuditHandler struct {
	listLogsUC  *application.ListLogsUseCase
	verifyChain *application.VerifyChainUseCase
}

func NewAuditHandler(
	listLogs *application.ListLogsUseCase,
	verifyChain *application.VerifyChainUseCase,
) *AuditHandler {
	return &AuditHandler{listLogsUC: listLogs, verifyChain: verifyChain}
}

// ListLogs retorna la bitácora del comercio autenticado. El tenant sale
// siempre del JWT: un tenant_id distinto en la query es un intento de leer
// otro comercio (403); la vista cross-tenant es ListPlatformLogs.
//
//	GET /api/v1/audit/logs?limit=50
func (h *AuditHandler) ListLogs(c *gin.Context) {
	tenantID, ok := postgres.TenantIDFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "tenant required"})
		return
	}
	if q := c.Query("tenant_id"); q != "" && q != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot read another tenant's audit log"})
		return
	}
	h.listLogs(c, tenantID)
}

// ListPlatformLogs es la vista de plataforma (platform_support): bitácora de
// un comercio con ?tenant_id=, o de toda la plataforma sin él.
//
//	GET /api/v1/audit/platform/logs?tenant_id=<uuid>&limit=50
func (h *AuditHandler) ListPlatformLogs(c *gin.Context) {
	h.listLogs(c, c.Query("tenant_id"))
}

func (h *AuditHandler) listLogs(c *gin.Context, tenantID string) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	views, err := h.listLogsUC.Execute(c.Request.Context(), application.ListLogsQuery{
		TenantID: tenantID,
		Limit:    limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": views,
		"count":   len(views),
	})
}

// VerifyChain verifica la integridad del hash chain del log.
//
//	GET /api/v1/audit/verify?from_id=0&limit=500
func (h *AuditHandler) VerifyChain(c *gin.Context) {
	fromID, _ := strconv.ParseInt(c.DefaultQuery("from_id", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))

	result, err := h.verifyChain.Execute(c.Request.Context(), application.VerifyChainQuery{
		FromID: fromID,
		Limit:  limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	status := http.StatusOK
	if !result.Valid {
		status = http.StatusConflict
	}

	c.JSON(status, result)
}

// RouteGuards autoriza cada operación de Audit. Se inyectan desde el
// composition root (cmd/api) para no acoplar este contexto con Auth; en
// producción son RequireRole(...).
type RouteGuards struct {
	ReadTenantLogs gin.HandlerFunc // GET /audit/logs (admin, platform_support)
	PlatformAudit  gin.HandlerFunc // GET /audit/platform/logs, /audit/verify (platform_support)
}

// RegisterRoutes registra las rutas del Audit en un grupo Gin existente.
// El grupo debe autenticar (JWT); los guards autorizan por rol. Todos son
// obligatorios: gin saltea los handlers nil, así que un guard olvidado dejaría
// la ruta abierta a cualquier usuario autenticado.
func RegisterRoutes(rg *gin.RouterGroup, handler *AuditHandler, guards RouteGuards) {
	if guards.ReadTenantLogs == nil || guards.PlatformAudit == nil {
		panic("audit: RegisterRoutes requires all RouteGuards (ReadTenantLogs, PlatformAudit)")
	}
	audit := rg.Group("/audit")
	{
		audit.GET("/logs", guards.ReadTenantLogs, handler.ListLogs)
		audit.GET("/platform/logs", guards.PlatformAudit, handler.ListPlatformLogs)
		audit.GET("/verify", guards.PlatformAudit, handler.VerifyChain)
	}
}
