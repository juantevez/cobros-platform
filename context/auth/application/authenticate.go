package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

const (
	accessTokenDuration  = 15 * time.Minute
	refreshTokenDuration = 7 * 24 * time.Hour
	accessTokenSeconds   = 15 * 60
)

// AuthenticateUseCase valida las credenciales de un usuario y emite un par de tokens.
//
// Flujo:
//  1. Verificar que el tenant existe y no está suspendido.
//  2. Buscar usuario por email en ese tenant.
//  3. Verificar que el usuario puede autenticarse (no suspendido).
//  4. Verificar la contraseña.
//  5. Obtener el rol del usuario en el tenant.
//  6. Emitir access token (JWT, 15 min) y refresh token (opaco, 7 días).
//  7. Persistir el refresh token hasheado.
//
// Nota de seguridad: si el email no existe, se retorna ErrInvalidCredentials
// (no ErrUserNotFound) para no revelar qué emails están registrados.
type AuthenticateUseCase struct {
	tenantRepo     TenantRepository
	userRepo       UserRepository
	membershipRepo MembershipRepository
	refreshRepo    RefreshTokenRepository
	hasher         PasswordHasher
	tokenIssuer    TokenIssuer
	clock          Clock
}

func NewAuthenticateUseCase(
	tenantRepo TenantRepository,
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	refreshRepo RefreshTokenRepository,
	hasher PasswordHasher,
	tokenIssuer TokenIssuer,
	clock Clock,
) *AuthenticateUseCase {
	return &AuthenticateUseCase{
		tenantRepo:     tenantRepo,
		userRepo:       userRepo,
		membershipRepo: membershipRepo,
		refreshRepo:    refreshRepo,
		hasher:         hasher,
		tokenIssuer:    tokenIssuer,
		clock:          clock,
	}
}

func (uc *AuthenticateUseCase) Execute(ctx context.Context, cmd AuthenticateCmd) (TokenPair, error) {
	// ── 1. Parsear inputs ────────────────────────────────────────────────────

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return TokenPair{}, err
	}

	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		// Email inválido → credentials inválidas (sin revelar qué falló)
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 2. Verificar tenant ──────────────────────────────────────────────────

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrTenantNotFound) {
			return TokenPair{}, domain.ErrInvalidCredentials
		}
		return TokenPair{}, fmt.Errorf("find tenant: %w", err)
	}
	if tenant.Status() == domain.TenantStatusSuspended {
		return TokenPair{}, domain.ErrTenantSuspended
	}

	// ── 3. Buscar usuario ────────────────────────────────────────────────────

	user, err := uc.userRepo.FindByEmail(ctx, tenantID, email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			// No revelar que el email no existe.
			return TokenPair{}, domain.ErrInvalidCredentials
		}
		return TokenPair{}, fmt.Errorf("find user: %w", err)
	}

	// ── 4. Verificar estado del usuario ──────────────────────────────────────

	if err := user.CanAuthenticate(); err != nil {
		return TokenPair{}, err // ErrUserSuspended
	}

	// ── 5. Verificar contraseña ──────────────────────────────────────────────

	valid, err := uc.hasher.Verify(cmd.Password, user.PasswordHash())
	if err != nil {
		return TokenPair{}, fmt.Errorf("verify password: %w", err)
	}
	if !valid {
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 6. Obtener rol del usuario ───────────────────────────────────────────

	membership, err := uc.membershipRepo.FindByUserAndTenant(ctx, user.ID(), tenantID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("find membership: %w", err)
	}

	// ── 7. Emitir access token ───────────────────────────────────────────────

	accessToken, err := uc.tokenIssuer.IssueAccessToken(AccessTokenClaims{
		UserID:      user.ID(),
		TenantID:    tenantID,
		Role:        membership.Role(),
		Environment: tenant.Environment(),
	})
	if err != nil {
		return TokenPair{}, fmt.Errorf("issue access token: %w", err)
	}

	// ── 8. Emitir y persistir refresh token ──────────────────────────────────

	rawRefresh, err := uc.tokenIssuer.IssueRefreshToken()
	if err != nil {
		return TokenPair{}, fmt.Errorf("issue refresh token: %w", err)
	}

	refreshHash, err := uc.hasher.Hash(rawRefresh)
	if err != nil {
		return TokenPair{}, fmt.Errorf("hash refresh token: %w", err)
	}

	now := uc.clock.Now()
	rt := RefreshToken{
		ID:        uuid.NewString(),
		UserID:    user.ID(),
		TenantID:  tenantID,
		TokenHash: refreshHash,
		IssuedAt:  now,
		ExpiresAt: now.Add(refreshTokenDuration),
	}

	if err := uc.refreshRepo.Save(ctx, rt); err != nil {
		return TokenPair{}, fmt.Errorf("save refresh token: %w", err)
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    accessTokenSeconds,
	}, nil
}
