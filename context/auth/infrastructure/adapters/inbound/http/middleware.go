package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// claimsKey es la clave para acceder a los claims en gin.Context.
const claimsKey = "auth_claims"

// ClaimsFromContext extrae los AccessTokenClaims del gin.Context.
// Los handlers lo usan para obtener el tenant y user del request.
func ClaimsFromContext(c *gin.Context) (application.AccessTokenClaims, bool) {
	v, exists := c.Get(claimsKey)
	if !exists {
		return application.AccessTokenClaims{}, false
	}
	claims, ok := v.(application.AccessTokenClaims)
	return claims, ok
}

// CorrelationIDMiddleware inyecta un correlation ID único en cada request.
// Si el cliente envía X-Correlation-ID, lo reutiliza; si no, genera uno nuevo.
func CorrelationIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		correlationID := c.GetHeader("X-Correlation-ID")
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		ctx := postgres.WithCorrelationID(c.Request.Context(), correlationID)
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Correlation-ID", correlationID)
		c.Next()
	}
}

// JWTMiddleware valida el Bearer token y puebla el contexto con tenant y actor.
// Rechaza con 401 si el token es inválido o está expirado, y con 403 si el
// tenant fue suspendido después de emitirlo (ver ensureTenantUsable).
func JWTMiddleware(tokenIssuer application.TokenIssuer, tenants application.TenantStatusChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "missing or invalid authorization header",
			})
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := tokenIssuer.VerifyAccessToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "invalid or expired token",
			})
			return
		}
		if !ensureTenantUsable(c, tenants, claims.TenantID) {
			return
		}

		// Propagar tenant y actor al contexto de Go.
		// El TxManager los extrae para configurar RLS y auditoría.
		ctx := postgres.WithTenantID(c.Request.Context(), claims.TenantID.String())
		ctx = postgres.WithActor(ctx, claims.UserID.String())
		c.Request = c.Request.WithContext(ctx)

		c.Set(claimsKey, claims)
		c.Next()
	}
}

// ApiKeyMiddleware valida el header X-Api-Key para integraciones server-to-server.
// Si el header no está presente, pasa al siguiente handler sin error
// (permite que JWTMiddleware maneje la autenticación en su lugar).
func ApiKeyMiddleware(
	apiKeyRepo application.ApiKeyRepository,
	hasher application.PasswordHasher,
	tenants application.TenantStatusChecker,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawKey := c.GetHeader("X-Api-Key")
		if rawKey == "" {
			c.Next()
			return
		}

		parsed, err := domain.ParseRawApiKey(rawKey)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "invalid api key format",
			})
			return
		}

		apiKey, err := apiKeyRepo.FindByPrefix(c.Request.Context(), parsed.Prefix)
		if err != nil {
			// No revelar si la key existe o no.
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "invalid api key",
			})
			return
		}

		// El <env> del header tiene que ser el de la key: la búsqueda es solo
		// por prefix, y sin esto una key test autenticaba como production_….
		if parsed.Environment != apiKey.Environment().String() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "invalid api key",
			})
			return
		}

		if apiKey.IsRevoked() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "api key has been revoked",
			})
			return
		}

		valid, err := hasher.Verify(parsed.Secret, apiKey.KeyHash())
		if err != nil || !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "invalid api key",
			})
			return
		}
		if !ensureTenantUsable(c, tenants, apiKey.TenantID()) {
			return
		}

		// Poblar contexto con el tenant de la API key. El actor identifica la
		// key para auditoría (no hay usuario detrás).
		ctx := postgres.WithTenantID(c.Request.Context(), apiKey.TenantID().String())
		ctx = postgres.WithActor(ctx, "apikey:"+apiKey.ID().String())
		c.Request = c.Request.WithContext(ctx)

		// Claims de integración: sin userID ni role; se autoriza por scopes.
		c.Set(claimsKey, application.AccessTokenClaims{
			TenantID:    apiKey.TenantID(),
			Environment: apiKey.Environment(),
			ApiKeyID:    apiKey.ID(),
			Scopes:      apiKey.Scopes(),
		})

		c.Next()
	}
}

// JWTOrApiKeyMiddleware autentica con X-Api-Key si el header está presente y,
// si no, con el Bearer JWT. Es para rutas que usan tanto el panel del comercio
// (JWT) como sus integraciones server-to-server (API key), por ejemplo cobrar.
// Combinarlo con RequirePermission para autorizar ambos casos.
func JWTOrApiKeyMiddleware(
	tokenIssuer application.TokenIssuer,
	apiKeyRepo application.ApiKeyRepository,
	hasher application.PasswordHasher,
	tenants application.TenantStatusChecker,
) gin.HandlerFunc {
	apiKeyMW := ApiKeyMiddleware(apiKeyRepo, hasher, tenants)
	jwtMW := JWTMiddleware(tokenIssuer, tenants)
	return func(c *gin.Context) {
		if c.GetHeader("X-Api-Key") != "" {
			apiKeyMW(c)
			return
		}
		jwtMW(c)
	}
}

// ensureTenantUsable corta el request si el tenant de la credencial fue
// suspendido (403) o ya no existe (401). Las credenciales siguen siendo
// válidas criptográficamente, así que sin este chequeo un tenant suspendido
// operaba hasta 15 minutos con su JWT y sin límite con sus API keys.
func ensureTenantUsable(c *gin.Context, tenants application.TenantStatusChecker, tenantID domain.TenantID) bool {
	err := tenants.EnsureNotSuspended(c.Request.Context(), tenantID)
	switch {
	case err == nil:
		return true
	case errors.Is(err, domain.ErrTenantSuspended):
		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{Error: domain.ErrTenantSuspended.Error()})
	case errors.Is(err, domain.ErrTenantNotFound):
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: "invalid credentials"})
	default:
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{Error: "internal server error"})
	}
	return false
}

// RequirePermission autoriza según cómo se autenticó el request:
//   - JWT: el rol debe tener la acción en la matriz de permisos del dominio.
//   - API key: la key debe tener al menos uno de los scopes indicados.
//
// Sin scopes, las API keys no pasan (la ruta es solo para usuarios).
func RequirePermission(action domain.Action, scopes ...domain.Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "authentication required",
			})
			return
		}

		if claims.IsApiKey() {
			for _, required := range scopes {
				for _, granted := range claims.Scopes {
					if granted == required {
						c.Next()
						return
					}
				}
			}
		} else if domain.RoleHasPermission(claims.Role, action) {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{
			Error: "insufficient permissions",
		})
	}
}

// RequireRole verifica que el usuario autenticado tiene el rol requerido.
// Debe usarse después de JWTMiddleware.
func RequireRole(roles ...domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "authentication required",
			})
			return
		}

		for _, required := range roles {
			if claims.Role == required {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{
			Error: "insufficient permissions",
		})
	}
}
