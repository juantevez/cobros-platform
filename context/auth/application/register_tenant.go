package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// RegisterTenantUseCase registra un nuevo comercio en la plataforma.
//
// El tenant se crea en estado Pending. No puede procesar pagos reales
// hasta que Onboarding (Fase 2) apruebe el KYC y lo active.
//
// La creación del tenant es inútil si nadie puede autenticarse contra él,
// así que el caso de uso crea además —en la misma transacción— el primer
// usuario con rol admin usando las credenciales del comando. Esto permite
// el flujo: POST /tenants → POST /auth/login sin pasos intermedios.
type RegisterTenantUseCase struct {
	tenantRepo     TenantRepository
	userRepo       UserRepository
	membershipRepo MembershipRepository
	hasher         PasswordHasher
	txManager      TxManager
	publisher      EventPublisher
}

func NewRegisterTenantUseCase(
	tenantRepo TenantRepository,
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	hasher PasswordHasher,
	txManager TxManager,
	publisher EventPublisher,
) *RegisterTenantUseCase {
	return &RegisterTenantUseCase{
		tenantRepo:     tenantRepo,
		userRepo:       userRepo,
		membershipRepo: membershipRepo,
		hasher:         hasher,
		txManager:      txManager,
		publisher:      publisher,
	}
}

func (uc *RegisterTenantUseCase) Execute(ctx context.Context, cmd RegisterTenantCmd) (RegisterTenantResult, error) {
	// ── 1. Validar inputs ──────────────────────────────────────────────────────

	tenantID := domain.NewTenantID()

	tenant, err := domain.NewTenant(tenantID, cmd.LegalName)
	if err != nil {
		return RegisterTenantResult{}, err // ErrEmptyLegalName
	}

	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		return RegisterTenantResult{}, err // ErrInvalidEmail
	}

	if err := domain.ValidatePassword(cmd.Password); err != nil {
		return RegisterTenantResult{}, err
	}

	// ── 2. Hashear el password fuera de la tx (bcrypt es lento) ────────────────

	passwordHash, err := uc.hasher.Hash(cmd.Password)
	if err != nil {
		return RegisterTenantResult{}, fmt.Errorf("hash password: %w", err)
	}

	// ── 3. Construir los agregados del admin inicial ───────────────────────────

	userID := domain.NewUserID()

	admin, err := domain.NewUser(userID, tenantID, email, passwordHash)
	if err != nil {
		return RegisterTenantResult{}, err
	}

	membership := domain.NewMembership(userID, tenantID, domain.RoleAdmin, domain.UserID(""))

	// ── 4. Persistir todo en una sola transacción ──────────────────────────────
	//
	// users/memberships están bajo RLS; RunInTx fija app.current_tenant recién
	// cuando el contexto ya trae un tenantID (p. ej. desde el middleware JWT).
	// En este flujo público no hay token, así que se inyecta explícitamente el
	// tenant recién creado para que el INSERT pase las políticas.
	txCtx := postgres.WithTenantID(ctx, tenantID.String())

	if err := uc.txManager.RunInTx(txCtx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Save(ctx, tenant); err != nil {
			return fmt.Errorf("save tenant: %w", err)
		}
		if err := uc.userRepo.Save(ctx, admin); err != nil {
			// El adaptador mapea unique(tenant_id, email) a ErrEmailAlreadyExists.
			if errors.Is(err, domain.ErrEmailAlreadyExists) {
				return domain.ErrEmailAlreadyExists
			}
			return fmt.Errorf("save admin user: %w", err)
		}
		if err := uc.membershipRepo.Save(ctx, membership); err != nil {
			return fmt.Errorf("save admin membership: %w", err)
		}
		return uc.publisher.Publish(ctx, append(tenant.PullEvents(), admin.PullEvents()...)...)
	}); err != nil {
		return RegisterTenantResult{}, err
	}

	return RegisterTenantResult{TenantID: tenantID.String(), UserID: userID.String()}, nil
}
