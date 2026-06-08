package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// RegisterUserUseCase registra un usuario en un tenant existente.
//
// El primer usuario de un tenant suele ser el administrador; el rol se define
// explícitamente en el comando. Los registros posteriores los hace un admin
// del tenant.
type RegisterUserUseCase struct {
	tenantRepo     TenantRepository
	userRepo       UserRepository
	membershipRepo MembershipRepository
	hasher         PasswordHasher
	txManager      TxManager
	publisher      EventPublisher
}

func NewRegisterUserUseCase(
	tenantRepo TenantRepository,
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	hasher PasswordHasher,
	txManager TxManager,
	publisher EventPublisher,
) *RegisterUserUseCase {
	return &RegisterUserUseCase{
		tenantRepo:     tenantRepo,
		userRepo:       userRepo,
		membershipRepo: membershipRepo,
		hasher:         hasher,
		txManager:      txManager,
		publisher:      publisher,
	}
}

func (uc *RegisterUserUseCase) Execute(ctx context.Context, cmd RegisterUserCmd) (RegisterUserResult, error) {
	// ── 1. Validar y parsear inputs ──────────────────────────────────────────

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return RegisterUserResult{}, err
	}

	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		return RegisterUserResult{}, err
	}

	if cmd.Password == "" {
		return RegisterUserResult{}, domain.ErrEmptyPassword
	}

	role, err := domain.ParseRole(cmd.Role)
	if err != nil {
		return RegisterUserResult{}, err
	}

	// ── 2. Verificar que el tenant existe y no está suspendido ───────────────

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return RegisterUserResult{}, fmt.Errorf("find tenant: %w", err)
	}
	if tenant.Status() == domain.TenantStatusSuspended {
		return RegisterUserResult{}, domain.ErrTenantSuspended
	}

	// ── 3. Hashear el password (fuera de la tx: puede ser lento) ─────────────

	passwordHash, err := uc.hasher.Hash(cmd.Password)
	if err != nil {
		return RegisterUserResult{}, fmt.Errorf("hash password: %w", err)
	}

	// ── 4. Construir los agregados ────────────────────────────────────────────

	userID := domain.NewUserID()

	user, err := domain.NewUser(userID, tenantID, email, passwordHash)
	if err != nil {
		return RegisterUserResult{}, err
	}

	membership := domain.NewMembership(userID, tenantID, role, domain.UserID(""))

	// ── 5. Persistir todo en una sola transacción ─────────────────────────────

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.userRepo.Save(ctx, user); err != nil {
			// El adaptador mapea la violación de unique(tenant_id, email)
			// a domain.ErrEmailAlreadyExists.
			if errors.Is(err, domain.ErrEmailAlreadyExists) {
				return domain.ErrEmailAlreadyExists
			}
			return fmt.Errorf("save user: %w", err)
		}

		if err := uc.membershipRepo.Save(ctx, membership); err != nil {
			return fmt.Errorf("save membership: %w", err)
		}

		// UserRegisteredEvent + RoleAssignedEvent (si membership lo emite)
		return uc.publisher.Publish(ctx, user.PullEvents()...)
	}); err != nil {
		return RegisterUserResult{}, err
	}

	return RegisterUserResult{UserID: userID.String()}, nil
}
