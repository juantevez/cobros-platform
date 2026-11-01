package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
// Anti-enumeración: el login no revela qué existe.
//   - Tenant inexistente, email inexistente o mal formado, contraseña
//     incorrecta o usuario sin membership → ErrInvalidCredentials.
//   - La contraseña se verifica siempre, también cuando no hay usuario
//     (contra un hash señuelo), para que el tiempo de respuesta no delate
//     si el email existe.
//   - Tenant o usuario suspendido (403) se informa solo después de validar
//     la contraseña: quien no la conoce no se entera de la suspensión.
type AuthenticateUseCase struct {
	tenantRepo     TenantRepository
	userRepo       UserRepository
	membershipRepo MembershipRepository
	refreshRepo    RefreshTokenRepository
	txManager      TxManager
	publisher      EventPublisher
	hasher         PasswordHasher
	tokenIssuer    TokenIssuer
	clock          Clock

	decoyOnce sync.Once
	decoyHash string
}

func NewAuthenticateUseCase(
	tenantRepo TenantRepository,
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	refreshRepo RefreshTokenRepository,
	hasher PasswordHasher,
	tokenIssuer TokenIssuer,
	clock Clock,
	txManager TxManager,
	publisher EventPublisher,
) *AuthenticateUseCase {
	return &AuthenticateUseCase{
		txManager:      txManager,
		publisher:      publisher,
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

	// ── 2. Buscar tenant y usuario; cualquier ausencia es "credenciales
	// inválidas", con la misma verificación de contraseña ──────────────────

	tenant, user, err := uc.findTenantAndUser(ctx, tenantID, cmd.Email)
	if err != nil {
		return TokenPair{}, err
	}
	if user == nil {
		uc.verifyDecoy(cmd.Password)
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 3. Verificar contraseña ──────────────────────────────────────────────

	valid, err := uc.hasher.Verify(cmd.Password, user.PasswordHash())
	if err != nil {
		return TokenPair{}, fmt.Errorf("verify password: %w", err)
	}
	if !valid {
		return TokenPair{}, domain.ErrInvalidCredentials
	}

	// ── 4. Estado del tenant y del usuario (solo tras validar la contraseña) ─

	if tenant.Status() == domain.TenantStatusSuspended {
		return TokenPair{}, domain.ErrTenantSuspended
	}
	if err := user.CanAuthenticate(); err != nil {
		return TokenPair{}, err // ErrUserSuspended
	}

	// ── 5. Obtener rol del usuario ───────────────────────────────────────────

	membership, err := uc.membershipRepo.FindByUserAndTenant(ctx, user.ID(), tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrMembershipNotFound) {
			// Usuario sin rol en el tenant: no puede entrar, y un 404 delataba
			// que la contraseña era correcta.
			return TokenPair{}, domain.ErrInvalidCredentials
		}
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

	now := uc.clock.Now()
	rt := RefreshToken{
		ID:        uuid.NewString(),
		UserID:    user.ID(),
		TenantID:  tenantID,
		TokenHash: hashRefreshToken(rawRefresh),
		IssuedAt:  now,
		ExpiresAt: now.Add(refreshTokenDuration),
	}

	// El refresh token y el evento de login (auditoría) se guardan juntos.
	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.refreshRepo.Save(ctx, rt); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
		return uc.publisher.Publish(ctx, domain.NewUserLoggedInEvent(tenantID, user.ID()))
	}); err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    accessTokenSeconds,
	}, nil
}

// findTenantAndUser busca el tenant y el usuario. Devuelve user nil (sin
// error) si alguno no existe o el email es inválido: para el login son todos
// "credenciales inválidas".
func (uc *AuthenticateUseCase) findTenantAndUser(ctx context.Context, tenantID domain.TenantID, rawEmail string) (*domain.Tenant, *domain.User, error) {
	email, err := domain.NewEmail(rawEmail)
	if err != nil {
		return nil, nil, nil
	}
	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if errors.Is(err, domain.ErrTenantNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find tenant: %w", err)
	}
	user, err := uc.userRepo.FindByEmail(ctx, tenantID, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find user: %w", err)
	}
	return tenant, user, nil
}

// verifyDecoy corre la verificación contra un hash señuelo (calculado una vez)
// para que un login sin usuario tarde lo mismo que uno con contraseña mala.
func (uc *AuthenticateUseCase) verifyDecoy(password string) {
	uc.decoyOnce.Do(func() {
		uc.decoyHash, _ = uc.hasher.Hash("decoy-password-for-timing")
	})
	if uc.decoyHash != "" {
		_, _ = uc.hasher.Verify(password, uc.decoyHash)
	}
}
