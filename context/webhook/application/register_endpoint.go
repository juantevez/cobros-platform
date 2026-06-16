package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// RegisterEndpointUseCase registra un nuevo endpoint webhook.
// Genera el secret HMAC y lo retorna una única vez.
type RegisterEndpointUseCase struct {
	endpointRepo EndpointRepository
	secretGen    SecretGenerator
	txManager    TxManager
	publisher    EventPublisher
}

func NewRegisterEndpointUseCase(
	endpointRepo EndpointRepository,
	secretGen SecretGenerator,
	txManager TxManager,
	publisher EventPublisher,
) *RegisterEndpointUseCase {
	return &RegisterEndpointUseCase{
		endpointRepo: endpointRepo,
		secretGen:    secretGen,
		txManager:    txManager,
		publisher:    publisher,
	}
}

func (uc *RegisterEndpointUseCase) Execute(ctx context.Context, cmd RegisterEndpointCmd) (RegisterEndpointResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return RegisterEndpointResult{}, err
	}

	secret, err := uc.secretGen.Generate()
	if err != nil {
		return RegisterEndpointResult{}, fmt.Errorf("generate secret: %w", err)
	}

	id := domain.NewEndpointID()
	endpoint, err := domain.NewWebhookEndpoint(id, tenantID, cmd.URL, secret, cmd.Events, cmd.Description)
	if err != nil {
		return RegisterEndpointResult{}, err
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.endpointRepo.Save(ctx, endpoint); err != nil {
			return fmt.Errorf("save endpoint: %w", err)
		}
		return uc.publisher.Publish(ctx, endpoint.PullEvents()...)
	}); err != nil {
		return RegisterEndpointResult{}, err
	}

	return RegisterEndpointResult{
		EndpointID: id.String(),
		Secret:     secret,
		SecretHint: endpoint.SecretHint(),
	}, nil
}

// ── DeactivateEndpoint ────────────────────────────────────────────────────────

// DeactivateEndpointUseCase desactiva un endpoint.
// Las deliveries pendientes no se enviarán a este endpoint.
type DeactivateEndpointUseCase struct {
	endpointRepo EndpointRepository
	txManager    TxManager
	publisher    EventPublisher
}

func NewDeactivateEndpointUseCase(
	endpointRepo EndpointRepository,
	txManager TxManager,
	publisher EventPublisher,
) *DeactivateEndpointUseCase {
	return &DeactivateEndpointUseCase{endpointRepo: endpointRepo, txManager: txManager, publisher: publisher}
}

func (uc *DeactivateEndpointUseCase) Execute(ctx context.Context, cmd DeactivateEndpointCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	endpointID, err := domain.ParseEndpointID(cmd.EndpointID)
	if err != nil {
		return err
	}

	endpoint, err := uc.endpointRepo.FindByID(ctx, endpointID)
	if err != nil {
		return err
	}
	if endpoint.TenantID() != tenantID {
		return domain.ErrEndpointNotFound
	}

	endpoint.Deactivate()

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.endpointRepo.Update(ctx, endpoint); err != nil {
			return fmt.Errorf("update endpoint: %w", err)
		}
		return uc.publisher.Publish(ctx, endpoint.PullEvents()...)
	})
}
