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
	listLogs    *application.ListLogsUseCase
	verifyChain *application.VerifyChainUseCase
}

func NewAuditHandler(
	listLogs *application.ListLogsUseCase,
	verifyChain *application.VerifyChainUseCase,
) *AuditHandler {
	return &AuditHandler{listLogs: listLogs, verifyChain: verifyChain}
}

// ListLogs retorna las últimas entradas del log.
//
//	GET /api/v1/audit/logs?limit=50
//	GET /api/v1/audit/logs?tenant_id=<uuid>&limit=50
func (h *AuditHandler) ListLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	// Si no se especifica tenant_id, usa el del JWT (aislamiento por defecto).
	tenantID := c.Query("tenant_id")
	if tenantID == "" {
		tenantID, _ = postgres.TenantIDFromContext(c.Request.Context())
	}

	views, err := h.listLogs.Execute(c.Request.Context(), application.ListLogsQuery{
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

// RegisterRoutes registra las rutas del Audit en un grupo Gin existente.
func RegisterRoutes(rg *gin.RouterGroup, handler *AuditHandler) {
	audit := rg.Group("/audit")
	{
		// Listar logs del tenant autenticado (admin) o de uno específico (platform_support).
		audit.GET("/logs", handler.ListLogs)
		// Verificar integridad del chain (solo platform_support).
		audit.GET("/verify", handler.VerifyChain)
	}
}
