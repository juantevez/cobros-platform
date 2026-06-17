package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// ReconciliationHandler expone la API de reconciliación al operador.
type ReconciliationHandler struct {
	startRun    *application.StartReconciliationUseCase
	processReport *application.ProcessReportUseCase
	processInternal *application.ProcessInternalUseCase
	resolveDisc *application.ResolveDiscrepancyUseCase
	getReport   *application.GetReportUseCase
	listRuns    *application.ListRunsUseCase
}

func NewReconciliationHandler(
	startRun *application.StartReconciliationUseCase,
	processReport *application.ProcessReportUseCase,
	processInternal *application.ProcessInternalUseCase,
	resolveDisc *application.ResolveDiscrepancyUseCase,
	getReport *application.GetReportUseCase,
	listRuns *application.ListRunsUseCase,
) *ReconciliationHandler {
	return &ReconciliationHandler{
		startRun: startRun, processReport: processReport,
		processInternal: processInternal, resolveDisc: resolveDisc,
		getReport: getReport, listRuns: listRuns,
	}
}

// StartRun inicia un run de reconciliación.
//
//	POST /api/v1/reconciliation/runs
func (h *ReconciliationHandler) StartRun(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req struct {
		Type       string `json:"type"        binding:"required"`
		PeriodFrom string `json:"period_from" binding:"required"`
		PeriodTo   string `json:"period_to"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	from, err := time.Parse(time.RFC3339, req.PeriodFrom)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_from: use RFC3339"})
		return
	}
	to, err := time.Parse(time.RFC3339, req.PeriodTo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_to: use RFC3339"})
		return
	}

	result, err := h.startRun.Execute(c.Request.Context(), application.StartReconciliationCmd{
		TenantID:   tenantID,
		Type:       req.Type,
		PeriodFrom: from,
		PeriodTo:   to,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"run_id": result.RunID})
}

// ListRuns lista los runs del tenant autenticado.
//
//	GET /api/v1/reconciliation/runs?limit=20
func (h *ReconciliationHandler) ListRuns(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	views, err := h.listRuns.Execute(c.Request.Context(), tenantID, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": views, "count": len(views)})
}

// GetReport retorna el reporte completo de un run con sus discrepancias.
//
//	GET /api/v1/reconciliation/runs/:runID?status=open&limit=100
func (h *ReconciliationHandler) GetReport(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	report, err := h.getReport.Execute(c.Request.Context(), application.GetReportQuery{
		RunID:        c.Param("runID"),
		StatusFilter: c.Query("status"),
		Limit:        limit,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// UploadReport procesa el CSV del PSP para un run de tipo "payment".
//
//	POST /api/v1/reconciliation/runs/:runID/report
//	Content-Type: text/csv
func (h *ReconciliationHandler) UploadReport(c *gin.Context) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 50*1024*1024)) // 50 MB max
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read request body"})
		return
	}
	if len(data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "report data is empty"})
		return
	}

	if err := h.processReport.Execute(c.Request.Context(), application.ProcessReportCmd{
		RunID:      c.Param("runID"),
		ReportData: data,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// ProcessInternal ejecuta la reconciliación interna del Ledger.
//
//	POST /api/v1/reconciliation/runs/:runID/process-internal
func (h *ReconciliationHandler) ProcessInternal(c *gin.Context) {
	if err := h.processInternal.Execute(c.Request.Context(), application.ProcessInternalCmd{
		RunID: c.Param("runID"),
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// ResolveDiscrepancy resuelve o ignora una discrepancia.
//
//	POST /api/v1/reconciliation/discrepancies/:discrepancyID/resolve
func (h *ReconciliationHandler) ResolveDiscrepancy(c *gin.Context) {
	var req struct {
		Action     string `json:"action"      binding:"required"` // "resolve" | "ignore"
		ResolvedBy string `json:"resolved_by" binding:"required"`
		Notes      string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.resolveDisc.Execute(c.Request.Context(), application.ResolveDiscrepancyCmd{
		DiscrepancyID: c.Param("discrepancyID"),
		Action:        req.Action,
		ResolvedBy:    req.ResolvedBy,
		Notes:         req.Notes,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrRunNotFound),
		errors.Is(err, domain.ErrDiscrepancyNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrRunAlreadyRunning),
		errors.Is(err, domain.ErrDiscrepancyAlreadyResolved):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidPeriod),
		errors.Is(err, domain.ErrEmptyReport),
		errors.Is(err, domain.ErrInvalidReportFormat):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RegisterRoutes registra las rutas de Reconciliation en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, h *ReconciliationHandler) {
	rec := rg.Group("/reconciliation")
	{
		rec.POST("/runs", h.StartRun)
		rec.GET("/runs", h.ListRuns)
		rec.GET("/runs/:runID", h.GetReport)
		rec.POST("/runs/:runID/report", h.UploadReport)
		rec.POST("/runs/:runID/process-internal", h.ProcessInternal)
		rec.POST("/discrepancies/:discrepancyID/resolve", h.ResolveDiscrepancy)
	}
}
