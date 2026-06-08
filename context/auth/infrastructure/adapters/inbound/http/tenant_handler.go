package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
)

// TenantHandler gestiona los endpoints de ciclo de vida de tenants.
type TenantHandler struct {
	register *application.RegisterTenantUseCase
	activate *application.ActivateTenantUseCase
	suspend  *application.SuspendTenantUseCase
}

func NewTenantHandler(
	register *application.RegisterTenantUseCase,
	activate *application.ActivateTenantUseCase,
	suspend *application.SuspendTenantUseCase,
) *TenantHandler {
	return &TenantHandler{
		register: register,
		activate: activate,
		suspend:  suspend,
	}
}

// ── Register ──────────────────────────────────────────────────────────────────

type registerTenantRequest struct {
	LegalName string `json:"legal_name" binding:"required"`
}

type registerTenantResponse struct {
	TenantID string `json:"tenant_id"`
}

// Register crea un nuevo comercio en estado Pending.
//
//	POST /api/v1/tenants
func (h *TenantHandler) Register(c *gin.Context) {
	var req registerTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.register.Execute(c.Request.Context(), application.RegisterTenantCmd{
		LegalName: req.LegalName,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusCreated, registerTenantResponse{TenantID: result.TenantID})
}

// ── Activate ──────────────────────────────────────────────────────────────────

type activateTenantRequest struct {
	Environment string `json:"environment" binding:"required"` // "test" | "production"
}

// Activate activa un tenant (operación de plataforma, requiere rol platform_support).
//
//	POST /api/v1/tenants/:tenantID/activate
func (h *TenantHandler) Activate(c *gin.Context) {
	var req activateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.activate.Execute(c.Request.Context(), application.ActivateTenantCmd{
		TenantID:    c.Param("tenantID"),
		Environment: req.Environment,
	}); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// ── Suspend ───────────────────────────────────────────────────────────────────

type suspendTenantRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// Suspend suspende un tenant (operación de plataforma).
//
//	POST /api/v1/tenants/:tenantID/suspend
func (h *TenantHandler) Suspend(c *gin.Context) {
	var req suspendTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.suspend.Execute(c.Request.Context(), application.SuspendTenantCmd{
		TenantID: c.Param("tenantID"),
		Reason:   req.Reason,
	}); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
