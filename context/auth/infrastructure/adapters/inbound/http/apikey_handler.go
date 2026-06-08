package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
)

// ApiKeyHandler gestiona la emisión y revocación de API keys.
type ApiKeyHandler struct {
	issue  *application.IssueApiKeyUseCase
	revoke *application.RevokeApiKeyUseCase
}

func NewApiKeyHandler(
	issue *application.IssueApiKeyUseCase,
	revoke *application.RevokeApiKeyUseCase,
) *ApiKeyHandler {
	return &ApiKeyHandler{issue: issue, revoke: revoke}
}

// ── Issue ─────────────────────────────────────────────────────────────────────

type issueApiKeyRequest struct {
	Name        string   `json:"name"        binding:"required"`
	Environment string   `json:"environment" binding:"required"`
	Scopes      []string `json:"scopes"      binding:"required,min=1"`
}

type issueApiKeyResponse struct {
	ApiKeyID string `json:"api_key_id"`
	// FullKey es la única oportunidad de ver la key completa. No se puede recuperar.
	FullKey string `json:"full_key"`
	Prefix  string `json:"prefix"`
}

// Issue genera una nueva API key para el tenant autenticado.
//
//	POST /api/v1/tenants/:tenantID/api-keys
func (h *ApiKeyHandler) Issue(c *gin.Context) {
	claims, ok := ClaimsFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	if c.Param("tenantID") != claims.TenantID.String() {
		respondError(c, http.StatusForbidden, "cannot manage api keys in another tenant")
		return
	}

	var req issueApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.issue.Execute(c.Request.Context(), application.IssueApiKeyCmd{
		TenantID:    claims.TenantID.String(),
		Name:        req.Name,
		Environment: req.Environment,
		Scopes:      req.Scopes,
		IssuedBy:    claims.UserID.String(),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusCreated, issueApiKeyResponse{
		ApiKeyID: result.ApiKeyID,
		FullKey:  result.FullKey,
		Prefix:   result.Prefix,
	})
}

// ── Revoke ────────────────────────────────────────────────────────────────────

// Revoke revoca una API key del tenant autenticado.
//
//	DELETE /api/v1/tenants/:tenantID/api-keys/:keyID
func (h *ApiKeyHandler) Revoke(c *gin.Context) {
	claims, ok := ClaimsFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	if c.Param("tenantID") != claims.TenantID.String() {
		respondError(c, http.StatusForbidden, "cannot manage api keys in another tenant")
		return
	}

	if err := h.revoke.Execute(c.Request.Context(), application.RevokeApiKeyCmd{
		TenantID:  claims.TenantID.String(),
		ApiKeyID:  c.Param("keyID"),
		RevokedBy: claims.UserID.String(),
	}); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
