package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// CreateAccountUseCase crea una cuenta contable para un tenant.
//
// Normalmente se llama al aprobar el onboarding de un comercio,
// para crear su cuenta merchant_balance en la moneda configurada.
// También se usa para crear cuentas internas de la plataforma.
//
// Es idempotente por (tenant, tipo, moneda): si la cuenta ya existe retorna
// su ID con WasExisting=true.
type CreateAccountUseCase struct {
	accountRepo AccountRepository
	txManager   TxManager
	publisher   EventPublisher
}

func NewCreateAccountUseCase(
	accountRepo AccountRepository,
	txManager TxManager,
	publisher EventPublisher,
) *CreateAccountUseCase {
	return &CreateAccountUseCase{
		accountRepo: accountRepo,
		txManager:   txManager,
		publisher:   publisher,
	}
}

func (uc *CreateAccountUseCase) Execute(ctx context.Context, cmd CreateAccountCmd) (CreateAccountResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return CreateAccountResult{}, err
	}

	accountType, err := domain.ParseAccountType(cmd.AccountType)
	if err != nil {
		return CreateAccountResult{}, err
	}

	id := domain.NewAccountID()
	account, err := domain.NewAccount(id, tenantID, accountType, cmd.Currency, cmd.Description)
	if err != nil {
		return CreateAccountResult{}, err
	}

	// Idempotencia: una cuenta por (tenant, tipo, moneda). Si ya existe se
	// retorna la existente sin error ni evento, para que los reintentos del
	// consumer de Onboarding (fallo parcial o redelivery) completen las cuentas
	// faltantes en vez de chocar contra el UNIQUE.
	existing, err := uc.findExisting(ctx, account)
	if err != nil {
		return CreateAccountResult{}, err
	}
	if existing != nil {
		return existingResult(existing), nil
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.accountRepo.Save(ctx, account); err != nil {
			return fmt.Errorf("save account: %w", err)
		}
		return uc.publisher.Publish(ctx, account.PullEvents()...)
	}); err != nil {
		// Carrera: otra ejecución la creó entre el chequeo y el INSERT.
		if errors.Is(err, domain.ErrAccountAlreadyExists) {
			if existing, findErr := uc.findExisting(ctx, account); findErr == nil && existing != nil {
				return existingResult(existing), nil
			}
		}
		return CreateAccountResult{}, err
	}

	return CreateAccountResult{AccountID: id.String()}, nil
}

// findExisting busca la cuenta con el mismo (tenant, tipo, moneda).
// Retorna (nil, nil) si no existe.
func (uc *CreateAccountUseCase) findExisting(ctx context.Context, a *domain.Account) (*domain.Account, error) {
	found, err := uc.accountRepo.FindByTenantAndType(ctx, a.TenantID(), a.AccountType(), a.Currency())
	if errors.Is(err, domain.ErrAccountNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find existing account: %w", err)
	}
	return found, nil
}

func existingResult(a *domain.Account) CreateAccountResult {
	return CreateAccountResult{AccountID: a.ID().String(), WasExisting: true}
}
