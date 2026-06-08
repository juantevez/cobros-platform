package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
)

// AuthHandler gestiona los endpoints de autenticación (login, refresh, logout).
type AuthHandler struct {
	authenticate *application.AuthenticateUseCase
	refreshToken *application.RefreshTokenUseCase
	logout       *application.LogoutUseCase
}

func NewAuthHandler(
	authenticate *application.AuthenticateUseCase,
	refreshToken *application.RefreshTokenUseCase,
	logout *application.LogoutUseCase,
) *AuthHandler {
	return &AuthHandler{
		authenticate: authenticate,
		refreshToken: refreshToken,
		logout:       logout,
	}
}

// ── Login ─────────────────────────────────────────────────────────────────────

type loginRequest struct {
	TenantID string `json:"tenant_id" binding:"required"`
	Email    string `json:"email"     binding:"required"`
	Password string `json:"password"  binding:"required"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // segundos
	TokenType    string `json:"token_type"`
}

// Login autentica a un usuario y retorna un par de tokens.
//
//	POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	pair, err := h.authenticate.Execute(c.Request.Context(), application.AuthenticateCmd{
		TenantID: req.TenantID,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusOK, tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    "Bearer",
	})
}

// ── Refresh ───────────────────────────────────────────────────────────────────

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh renueva el par de tokens usando un refresh token válido.
//
//	POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	pair, err := h.refreshToken.Execute(c.Request.Context(), application.RefreshTokenCmd{
		RawRefreshToken: req.RefreshToken,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusOK, tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    "Bearer",
	})
}

// ── Logout ────────────────────────────────────────────────────────────────────

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout revoca el refresh token activo del usuario.
//
//	POST /api/v1/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.logout.Execute(c.Request.Context(), application.LogoutCmd{
		RawRefreshToken: req.RefreshToken,
	}); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
