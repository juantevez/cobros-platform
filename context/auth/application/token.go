package application

import (
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// AccessTokenClaims contiene los claims que se codifican en el JWT de acceso.
//
// Estos datos están disponibles sin consultar la base en cada request,
// por eso se elige qué incluir con cuidado: deben ser estables durante
// la vida del token (15 min) y no demasiado sensibles.
type AccessTokenClaims struct {
	UserID      domain.UserID
	TenantID    domain.TenantID
	Role        domain.Role
	Environment domain.Environment

	// ApiKeyID y Scopes solo se completan cuando el request se autenticó con
	// una API key (ApiKeyMiddleware); no viajan en el JWT. En ese caso no hay
	// UserID ni Role: la autorización se hace por scopes.
	ApiKeyID domain.ApiKeyID
	Scopes   []domain.Scope
}

// IsApiKey indica si los claims provienen de una API key y no de un JWT.
func (c AccessTokenClaims) IsApiKey() bool { return c.ApiKeyID != "" }

// ErrRefreshTokenNotFound: no hay refresh token con ese hash.
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// errRefreshTokenReused corta la tx de rotación cuando el token ya estaba
// rotado (reuso o carrera): el caso de uso revoca todas las sesiones.
var errRefreshTokenReused = errors.New("refresh token already rotated")

// RefreshToken es un token de larga duración para renovar el access token.
//
// No es un agregado de dominio: es un artefacto de la capa de autenticación.
// El secreto nunca se almacena en claro; solo el hash se persiste.
//
// Rotación: al usar un refresh token se revoca el anterior y se emite uno nuevo.
// Si un token ya rotado se presenta de nuevo (posible robo), se revocan todas
// las sesiones del usuario.
type RefreshToken struct {
	ID         string
	UserID     domain.UserID
	TenantID   domain.TenantID
	TokenHash  string     // hash del secreto aleatorio
	IssuedAt   time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *string    // ID del token sucesor (tras rotación)
}

// IsExpired retorna true si el token pasó su fecha de expiración.
func (rt *RefreshToken) IsExpired(now time.Time) bool {
	return now.After(rt.ExpiresAt)
}

// IsRotated retorna true si el token fue reemplazado por otro (no por logout).
func (rt *RefreshToken) IsRotated() bool {
	return rt.ReplacedBy != nil
}

// IsRevoked retorna true si el token fue revocado (por rotación o logout).
func (rt *RefreshToken) IsRevoked() bool {
	return rt.RevokedAt != nil
}

// IsValid retorna true si el token puede usarse para renovar el acceso.
func (rt *RefreshToken) IsValid(now time.Time) bool {
	return !rt.IsRevoked() && !rt.IsExpired(now)
}

// TokenPair es el resultado de autenticación o renovación.
type TokenPair struct {
	AccessToken  string
	RefreshToken string // secreto en claro, solo en este momento
	ExpiresIn    int    // segundos hasta expiración del access token
}
