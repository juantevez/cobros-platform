package http

import (
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
// Rechaza con 401 si el token es inválido o está expirado.
func JWTMiddleware(tokenIssuer application.TokenIssuer) gin.HandlerFunc {
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

		// Poblar contexto con el tenant de la API key.
		ctx := postgres.WithTenantID(c.Request.Context(), apiKey.TenantID().String())
		c.Request = c.Request.WithContext(ctx)

		// Guardar claims parciales (sin userID, sin role: es una integración).
		c.Set(claimsKey, application.AccessTokenClaims{
			TenantID:    apiKey.TenantID(),
			Environment: apiKey.Environment(),
		})

		c.Next()
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
