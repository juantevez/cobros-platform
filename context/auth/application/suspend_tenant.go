package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// SuspendTenantUseCase suspende un comercio por decisión del operador de la plataforma.
// Los demás contextos (Ledger, Payouts) deben reaccionar al evento y bloquear
// operaciones del tenant suspendido.
type SuspendTenantUseCase struct {
	tenantRepo TenantRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewSuspendTenantUseCase(
	tenantRepo TenantRepository,
	txManager TxManager,
	publisher EventPublisher,
) *SuspendTenantUseCase {
	return &SuspendTenantUseCase{
		tenantRepo: tenantRepo,
		txManager:  txManager,
		publisher:  publisher,
	}
}

func (uc *SuspendTenantUseCase) Execute(ctx context.Context, cmd SuspendTenantCmd) error {
	if cmd.Reason == "" {
		return fmt.Errorf("suspension reason is required")
	}

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}

	if err := tenant.Suspend(cmd.Reason); err != nil {
		return err // ErrTenantCannotTransition
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Update(ctx, tenant); err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, tenant.PullEvents()...)
	})
}
