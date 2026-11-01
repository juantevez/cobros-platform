package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// RefreshTokenUseCase renueva un par de tokens usando un refresh token válido.
//
// Implementa rotación: el token presentado se revoca y se emite uno nuevo.
// Si el token presentado ya estaba revocado (indicador de posible robo),
// se retorna ErrInvalidCredentials sin emitir tokens nuevos.
//
// La rotación garantiza que un token robado sea inútil después de que el
// usuario legítimo lo use una vez.
type RefreshTokenUseCase struct {
	userRepo       UserRepository
	membershipRepo MembershipRepository
	tenantRepo     TenantRepository
	refreshRepo    RefreshTokenRepository
	tokenIssuer    TokenIssuer
	clock          Clock
}

func NewRefreshTokenUseCase(
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	tenantRepo TenantRepository,
	refreshRepo RefreshTokenRepository,
	tokenIssuer TokenIssuer,
	clock Clock,
) *RefreshTokenUseCase {
	return &RefreshTokenUseCase{
		userRepo:       userRepo,
		membershipRepo: membershipRepo,
		tenantRepo:     tenantRepo,
		refreshRepo:    refreshRepo,
		tokenIssuer:    tokenIssuer,
		clock:          clock,
	}
}

func (uc *RefreshTokenUseCase) Execute(ctx context.Context, cmd RefreshTokenCmd) (TokenPair, error) {
	if cmd.RawRefreshToken == "" {
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 1. Hashear el token presentado y buscarlo ────────────────────────────
	// Hasheamos el raw para buscar por hash (nunca almacenamos en claro).

	stored, err := uc.refreshRepo.FindByHash(ctx, hashRefreshToken(cmd.RawRefreshToken))
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return TokenPair{}, domain.ErrInvalidCredentials
		}
		return TokenPair{}, fmt.Errorf("find refresh token: %w", err)
	}

	// ── 2. Validar el token ──────────────────────────────────────────────────

	now := uc.clock.Now()

	if stored.IsRevoked() {
		// Token ya revocado: posible robo de token.
		// En una implementación completa, se invalidarían todos los tokens
		// de la familia. Para Fase 1, rechazamos la operación.
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	if stored.IsExpired(now) {
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 3. Verificar que el usuario y tenant siguen activos ──────────────────
	// El request es público (sin JWT): el tenant sale del token guardado, y
	// users/memberships solo se leen en la sesión RLS de ese tenant.
	ctx = postgres.WithTenantID(ctx, stored.TenantID.String())

	user, err := uc.userRepo.FindByID(ctx, stored.UserID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("find user: %w", err)
	}
	if err := user.CanAuthenticate(); err != nil {
		return TokenPair{}, err
	}

	tenant, err := uc.tenantRepo.FindByID(ctx, stored.TenantID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("find tenant: %w", err)
	}
	if tenant.Status() == domain.TenantStatusSuspended {
		return TokenPair{}, domain.ErrTenantSuspended
	}

	membership, err := uc.membershipRepo.FindByUserAndTenant(ctx, user.ID(), stored.TenantID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("find membership: %w", err)
	}

	// ── 4. Emitir nuevo access token con claims actualizados ─────────────────

	accessToken, err := uc.tokenIssuer.IssueAccessToken(AccessTokenClaims{
		UserID:      user.ID(),
		TenantID:    stored.TenantID,
		Role:        membership.Role(),
		Environment: tenant.Environment(),
	})
	if err != nil {
		return TokenPair{}, fmt.Errorf("issue access token: %w", err)
	}

	// ── 5. Generar nuevo refresh token y rotar ───────────────────────────────

	rawRefresh, err := uc.tokenIssuer.IssueRefreshToken()
	if err != nil {
		return TokenPair{}, fmt.Errorf("issue refresh token: %w", err)
	}

	newID := uuid.NewString()
	newToken := RefreshToken{
		ID:        newID,
		UserID:    stored.UserID,
		TenantID:  stored.TenantID,
		TokenHash: hashRefreshToken(rawRefresh),
		IssuedAt:  now,
		ExpiresAt: now.Add(refreshTokenDuration),
	}

	if err := uc.refreshRepo.Save(ctx, newToken); err != nil {
		return TokenPair{}, fmt.Errorf("save new refresh token: %w", err)
	}

	// Revocar el token anterior, registrando su sucesor.
	if err := uc.refreshRepo.Revoke(ctx, stored.ID, newID); err != nil {
		return TokenPair{}, fmt.Errorf("revoke old refresh token: %w", err)
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    accessTokenSeconds,
	}, nil
}

// LogoutUseCase revoca el refresh token activo del usuario, cerrando la sesión.
type LogoutUseCase struct {
	refreshRepo RefreshTokenRepository
	txManager   TxManager
	publisher   EventPublisher
}

func NewLogoutUseCase(refreshRepo RefreshTokenRepository, txManager TxManager, publisher EventPublisher) *LogoutUseCase {
	return &LogoutUseCase{refreshRepo: refreshRepo, txManager: txManager, publisher: publisher}
}

func (uc *LogoutUseCase) Execute(ctx context.Context, cmd LogoutCmd) error {
	if cmd.RawRefreshToken == "" {
		return nil // idempotente: sin token, ya está "deslogueado"
	}

	stored, err := uc.refreshRepo.FindByHash(ctx, hashRefreshToken(cmd.RawRefreshToken))
	if err != nil {
		return nil // token no encontrado = ya expiró o nunca existió
	}

	if stored.IsRevoked() {
		return nil // ya estaba revocado
	}

	// La revocación y el evento de logout (auditoría) se guardan juntos.
	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.refreshRepo.Revoke(ctx, stored.ID, ""); err != nil {
			return err
		}
		return uc.publisher.Publish(ctx, domain.NewUserLoggedOutEvent(stored.TenantID, stored.UserID))
	})
}
