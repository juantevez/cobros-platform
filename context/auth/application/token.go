package application

import (
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
}

// RefreshToken es un token de larga duración para renovar el access token.
//
// No es un agregado de dominio: es un artefacto de la capa de autenticación.
// El secreto nunca se almacena en claro; solo el hash se persiste.
//
// Rotación: al usar un refresh token se revoca el anterior y se emite uno nuevo.
// Si un token ya revocado se presenta de nuevo, se revoca toda la familia
// (indica posible robo del token).
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
