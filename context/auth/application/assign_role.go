package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// AssignRoleUseCase asigna o actualiza el rol de un usuario en un tenant.
//
// Si el usuario ya tiene una membership, la actualiza.
// Si no tiene, la crea. Esto simplifica el flujo del cliente (upsert semántico).
type AssignRoleUseCase struct {
	tenantRepo     TenantRepository
	userRepo       UserRepository
	membershipRepo MembershipRepository
	txManager      TxManager
	publisher      EventPublisher
}

func NewAssignRoleUseCase(
	tenantRepo TenantRepository,
	userRepo UserRepository,
	membershipRepo MembershipRepository,
	txManager TxManager,
	publisher EventPublisher,
) *AssignRoleUseCase {
	return &AssignRoleUseCase{
		tenantRepo:     tenantRepo,
		userRepo:       userRepo,
		membershipRepo: membershipRepo,
		txManager:      txManager,
		publisher:      publisher,
	}
}

func (uc *AssignRoleUseCase) Execute(ctx context.Context, cmd AssignRoleCmd) error {
	// ── 1. Validar inputs ────────────────────────────────────────────────────

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	userID, err := domain.ParseUserID(cmd.UserID)
	if err != nil {
		return err
	}

	assignedBy, err := domain.ParseUserID(cmd.AssignedBy)
	if err != nil {
		return err
	}

	role, err := domain.ParseRole(cmd.Role)
	if err != nil {
		return err
	}

	// ── 2. Verificar que el tenant está activo ───────────────────────────────

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}
	if !tenant.IsActive() {
		return domain.ErrTenantNotActive
	}

	// ── 3. Verificar que el usuario existe y pertenece al tenant ─────────────

	user, err := uc.userRepo.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	if user.TenantID() != tenantID {
		return domain.ErrUserNotFound // no revelar que existe en otro tenant
	}

	// ── 4. Upsert de la membership ───────────────────────────────────────────

	existing, err := uc.membershipRepo.FindByUserAndTenant(ctx, userID, tenantID)
	if err != nil && !errors.Is(err, domain.ErrMembershipNotFound) {
		return fmt.Errorf("find membership: %w", err)
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if errors.Is(err, domain.ErrMembershipNotFound) {
			m := domain.NewMembership(userID, tenantID, role, assignedBy)
			if saveErr := uc.membershipRepo.Save(ctx, m); saveErr != nil {
				return fmt.Errorf("save membership: %w", saveErr)
			}
		} else {
			existing.UpdateRole(role, assignedBy)
			if updateErr := uc.membershipRepo.Update(ctx, *existing); updateErr != nil {
				return fmt.Errorf("update membership: %w", updateErr)
			}
		}

		ev := domain.NewRoleAssignedEvent(
			tenantID.String(),
			userID.String(),
			role.String(),
			assignedBy.String(),
		)
		return uc.publisher.Publish(ctx, ev)
	})
}
