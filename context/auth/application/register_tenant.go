package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// RegisterTenantUseCase registra un nuevo comercio en la plataforma.
//
// El tenant se crea en estado Pending. No puede procesar pagos reales
// hasta que Onboarding (Fase 2) apruebe el KYC y lo active.
type RegisterTenantUseCase struct {
	tenantRepo TenantRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewRegisterTenantUseCase(
	tenantRepo TenantRepository,
	txManager TxManager,
	publisher EventPublisher,
) *RegisterTenantUseCase {
	return &RegisterTenantUseCase{
		tenantRepo: tenantRepo,
		txManager:  txManager,
		publisher:  publisher,
	}
}

func (uc *RegisterTenantUseCase) Execute(ctx context.Context, cmd RegisterTenantCmd) (RegisterTenantResult, error) {
	id := domain.NewTenantID()

	tenant, err := domain.NewTenant(id, cmd.LegalName)
	if err != nil {
		return RegisterTenantResult{}, err // ErrEmptyLegalName
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Save(ctx, tenant); err != nil {
			return fmt.Errorf("save tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, tenant.PullEvents()...)
	}); err != nil {
		return RegisterTenantResult{}, err
	}

	return RegisterTenantResult{TenantID: id.String()}, nil
}
