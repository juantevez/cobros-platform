package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// ActivateTenantUseCase activa un comercio o lo pasa de test a producción. Lo
// invocan el operador (HTTP) y el consumer de aprobación de Onboarding; es
// idempotente si el tenant ya está activo en ese ambiente.
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
	events := tenant.PullEvents()
	if len(events) == 0 {
		return nil // ya estaba activo en ese ambiente
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Update(ctx, tenant); err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, events...)
	})
}

// ReactivateTenantUseCase levanta la suspensión de un comercio. La suspensión
// por cumplimiento solo la levanta un compliance_officer; la del operador,
// platform_support. Los holds de cumplimiento vigentes no se tocan: Payment y
// Payout siguen bloqueados hasta que cumplimiento los libere.
type ReactivateTenantUseCase struct {
	tenantRepo TenantRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewReactivateTenantUseCase(tenantRepo TenantRepository, txManager TxManager, publisher EventPublisher) *ReactivateTenantUseCase {
	return &ReactivateTenantUseCase{tenantRepo: tenantRepo, txManager: txManager, publisher: publisher}
}

func (uc *ReactivateTenantUseCase) Execute(ctx context.Context, cmd ReactivateTenantCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}
	if err := tenant.Reactivate(cmd.ByCompliance); err != nil {
		return err // ErrTenantCannotTransition / ErrReactivationNotAllowed
	}
	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Update(ctx, tenant); err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, tenant.PullEvents()...)
	})
}
