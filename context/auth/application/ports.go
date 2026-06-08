package application

// ports.go define los puertos de salida del contexto Auth.
//
// En arquitectura hexagonal, los puertos de salida son las interfaces que
// el núcleo de la aplicación define en sus propios términos y que los
// adaptadores de infraestructura (Postgres, NATS, JWT) implementan.
//
// Regla: ninguna interface aquí debe importar tipos de infraestructura.
// Solo el dominio y la stdlib están permitidos.

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// ── Transaction Manager ───────────────────────────────────────────────────────

// TxManager abstrae el manejo de transacciones de base de datos.
// La implementación concreta vive en pkg/postgres.TxManager.
// Los casos de uso lo usan para garantizar atomicidad entre Save y Publish.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// ── Repositorios ──────────────────────────────────────────────────────────────

// TenantRepository persiste y recupera el agregado Tenant.
type TenantRepository interface {
	Save(ctx context.Context, t *domain.Tenant) error
	Update(ctx context.Context, t *domain.Tenant) error
	FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error)
}

// UserRepository persiste y recupera el agregado User.
type UserRepository interface {
	Save(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	FindByID(ctx context.Context, id domain.UserID) (*domain.User, error)
	FindByEmail(ctx context.Context, tenantID domain.TenantID, email domain.Email) (*domain.User, error)
}

// MembershipRepository persiste y recupera la entidad Membership.
type MembershipRepository interface {
	Save(ctx context.Context, m domain.Membership) error
	Update(ctx context.Context, m domain.Membership) error
	FindByUserAndTenant(ctx context.Context, userID domain.UserID, tenantID domain.TenantID) (*domain.Membership, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]domain.Membership, error)
}

// ApiKeyRepository persiste y recupera el agregado ApiKey.
type ApiKeyRepository interface {
	Save(ctx context.Context, k *domain.ApiKey) error
	Update(ctx context.Context, k *domain.ApiKey) error
	FindByID(ctx context.Context, id domain.ApiKeyID) (*domain.ApiKey, error)
	// FindByPrefix busca por el prefix visible de la key (para autenticación).
	FindByPrefix(ctx context.Context, prefix string) (*domain.ApiKey, error)
}

// RefreshTokenRepository persiste y recupera refresh tokens.
type RefreshTokenRepository interface {
	Save(ctx context.Context, token RefreshToken) error
	// FindByHash busca un token por el hash del secreto que presentó el cliente.
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// Revoke marca un token como revocado y registra su reemplazo.
	Revoke(ctx context.Context, tokenID string, replacedBy string) error
}

// ── Servicios de soporte ──────────────────────────────────────────────────────

// PasswordHasher abstrae el algoritmo de hash y verificación de contraseñas.
// La implementación concreta usa argon2id para hash y verificación constante en tiempo.
type PasswordHasher interface {
	// Hash calcula el hash de plaintext. Cada llamada produce un resultado distinto
	// por el salt aleatorio interno.
	Hash(plaintext string) (string, error)
	// Verify compara plaintext contra hash. Retorna true si coinciden.
	// Es de tiempo constante para prevenir timing attacks.
	Verify(plaintext, hash string) (bool, error)
}

// TokenIssuer emite y verifica tokens de autenticación JWT.
type TokenIssuer interface {
	// IssueAccessToken genera un JWT de corta duración (15 min) con los claims dados.
	IssueAccessToken(claims AccessTokenClaims) (string, error)
	// IssueRefreshToken genera un secreto aleatorio para el refresh token.
	// El caller es responsable de hashearlo antes de persistirlo.
	IssueRefreshToken() (string, error)
	// VerifyAccessToken valida la firma y expiración del JWT y retorna los claims.
	VerifyAccessToken(tokenStr string) (AccessTokenClaims, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox transaccional.
// La implementación escribe en outbox_messages dentro de la tx activa del contexto.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el acceso al tiempo. Facilita tests deterministas.
type Clock interface {
	Now() time.Time
}
