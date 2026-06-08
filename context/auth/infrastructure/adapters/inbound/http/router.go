package http

import (
	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// NewRouter configura y retorna el engine de Gin con todas las rutas del contexto Auth.
//
// Convención de rutas:
//
//	Públicas:   POST /api/v1/tenants, POST /api/v1/auth/*
//	Protegidas: requieren Bearer JWT o X-Api-Key
//	De admin:   requieren además rol admin o platform_support
func NewRouter(
	tokenIssuer application.TokenIssuer,
	apiKeyRepo application.ApiKeyRepository,
	hasher application.PasswordHasher,
	tenantHandler *TenantHandler,
	authHandler *AuthHandler,
	userHandler *UserHandler,
	apiKeyHandler *ApiKeyHandler,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(CorrelationIDMiddleware())

	v1 := r.Group("/api/v1")

	// ── Rutas públicas (sin autenticación) ───────────────────────────────────

	// Registro de nuevos comercios (entrada al sistema).
	v1.POST("/tenants", tenantHandler.Register)

	// Autenticación y renovación de tokens.
	auth := v1.Group("/auth")
	{
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)
		auth.POST("/logout", authHandler.Logout)
	}

	// ── Rutas protegidas por JWT ──────────────────────────────────────────────

	jwtMW := JWTMiddleware(tokenIssuer)
	apiKeyMW := ApiKeyMiddleware(apiKeyRepo, hasher)

	protected := v1.Group("")
	// Acepta JWT o X-Api-Key (el primero que autentique gana).
	// Para simplificar Fase 1: usamos solo JWT en rutas de admin.
	protected.Use(jwtMW)
	{
		// Gestión de usuarios del tenant (solo admins).
		tenants := protected.Group("/tenants/:tenantID")
		tenants.Use(RequireRole(domain.RoleAdmin, domain.RolePlatformSupport))
		{
			tenants.POST("/users", userHandler.Register)
			tenants.PUT("/members/:userID/role", userHandler.AssignRole)
			tenants.POST("/api-keys", apiKeyHandler.Issue)
			tenants.DELETE("/api-keys/:keyID", apiKeyHandler.Revoke)
		}

		// Operaciones de plataforma (solo platform_support).
		platform := protected.Group("/tenants/:tenantID")
		platform.Use(RequireRole(domain.RolePlatformSupport))
		{
			platform.POST("/activate", tenantHandler.Activate)
			platform.POST("/suspend", tenantHandler.Suspend)
		}
	}

	// ── Rutas para integraciones server-to-server (ApiKey) ───────────────────
	// Ejemplo: crear un cobro desde el backend del comercio.
	// Se expande en Fase 2 con Payment Processing.
	integrations := v1.Group("/integrations")
	integrations.Use(apiKeyMW)
	{
		// Placeholder para Fase 2.
		_ = integrations
	}

	return r
}
