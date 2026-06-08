package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// RevokeApiKeyUseCase revoca una API key existente.
// Una key revocada no puede usarse ni reactivarse.
type RevokeApiKeyUseCase struct {
	apiKeyRepo ApiKeyRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewRevokeApiKeyUseCase(
	apiKeyRepo ApiKeyRepository,
	txManager TxManager,
	publisher EventPublisher,
) *RevokeApiKeyUseCase {
	return &RevokeApiKeyUseCase{
		apiKeyRepo: apiKeyRepo,
		txManager:  txManager,
		publisher:  publisher,
	}
}

func (uc *RevokeApiKeyUseCase) Execute(ctx context.Context, cmd RevokeApiKeyCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	apiKeyID, err := domain.ParseApiKeyID(cmd.ApiKeyID)
	if err != nil {
		return err
	}

	apiKey, err := uc.apiKeyRepo.FindByID(ctx, apiKeyID)
	if err != nil {
		return fmt.Errorf("find api key: %w", err)
	}

	// Verificar que la key pertenece al tenant del caller (aislamiento).
	if apiKey.TenantID() != tenantID {
		return domain.ErrApiKeyNotFound // no revelar que existe en otro tenant
	}

	if err := apiKey.Revoke(); err != nil {
		return err // ErrApiKeyAlreadyRevoked
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.apiKeyRepo.Update(ctx, apiKey); err != nil {
			return fmt.Errorf("update api key: %w", err)
		}
		return uc.publisher.Publish(ctx, apiKey.PullEvents()...)
	})
}
