package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// ReportingHandler expone las consultas del dashboard sobre el read-model.
type ReportingHandler struct {
	reports *application.GetReportsUseCase
}

func NewReportingHandler(reports *application.GetReportsUseCase) *ReportingHandler {
	return &ReportingHandler{reports: reports}
}

// GetVolume retorna la serie de volumen transaccional.
//
//	GET /api/v1/reports/volume?from=&to=&granularity=day
func (h *ReportingHandler) GetVolume(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	gran, err := domain.ParseGranularity(c.Query("granularity"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	points, err := h.reports.GetVolume(c.Request.Context(), application.VolumeQuery{
		TenantID: tenantID, From: from, To: to, Granularity: gran,
	})
	if err != nil {
		respondReportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"granularity": gran.String(), "points": points})
}

// GetRevenue retorna el resumen de comisiones por período.
//
//	GET /api/v1/reports/revenue?from=&to=
func (h *ReportingHandler) GetRevenue(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	from, to, ok := parseRange(c)
	if !ok {
		return
	}

	summary, err := h.reports.GetRevenue(c.Request.Context(), application.RevenueQuery{
		TenantID: tenantID, From: from, To: to,
	})
	if err != nil {
		respondReportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revenue": summary})
}

// GetBalances retorna el saldo neto por tipo de cuenta del comercio.
//
//	GET /api/v1/reports/balances
func (h *ReportingHandler) GetBalances(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	balances, err := h.reports.GetBalances(c.Request.Context(), tenantID)
	if err != nil {
		respondReportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"balances": balances})
}

// parseRange lee los parámetros from/to en formato RFC3339. Vacíos = sin filtro.
// Retorna ok=false y responde 400 si el formato es inválido.
func parseRange(c *gin.Context) (from, to time.Time, ok bool) {
	if s := c.Query("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from': use RFC3339"})
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if s := c.Query("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to': use RFC3339"})
			return time.Time{}, time.Time{}, false
		}
		to = t
	}
	return from, to, true
}

func respondReportError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrInvalidRange) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

// RegisterRoutes registra las rutas de Reporting en el grupo protegido.
func RegisterRoutes(rg *gin.RouterGroup, h *ReportingHandler) {
	reports := rg.Group("/reports")
	{
		reports.GET("/volume", h.GetVolume)
		reports.GET("/revenue", h.GetRevenue)
		reports.GET("/balances", h.GetBalances)
	}
}
