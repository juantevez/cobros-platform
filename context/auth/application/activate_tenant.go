package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// ActivateTenantUseCase activa un comercio tras la aprobación del KYC.
//
// En Fase 1 puede invocarse directamente por el operador.
// En Fase 2 será disparado por el evento de aprobación del módulo Onboarding.
type ActivateTenantUseCase struct {
	tenantRepo TenantRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewActivateTenantUseCase(
	tenantRepo TenantRepository,
	txManager TxManager,
	publisher EventPublisher,
) *ActivateTenantUseCase {
	return &ActivateTenantUseCase{
		tenantRepo: tenantRepo,
		txManager:  txManager,
		publisher:  publisher,
	}
}

func (uc *ActivateTenantUseCase) Execute(ctx context.Context, cmd ActivateTenantCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	env, err := domain.ParseEnvironment(cmd.Environment)
	if err != nil {
		return err
	}

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}

	if err := tenant.Activate(env); err != nil {
		return err // ErrTenantCannotTransition
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Update(ctx, tenant); err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, tenant.PullEvents()...)
	})
}
