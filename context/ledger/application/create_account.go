package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// CreateAccountUseCase crea una cuenta contable para un tenant.
//
// Normalmente se llama al aprobar el onboarding de un comercio,
// para crear su cuenta merchant_balance en la moneda configurada.
// También se usa para crear cuentas internas de la plataforma.
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

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.accountRepo.Save(ctx, account); err != nil {
			return fmt.Errorf("save account: %w", err)
		}
		return uc.publisher.Publish(ctx, account.PullEvents()...)
	}); err != nil {
		return CreateAccountResult{}, err
	}

	return CreateAccountResult{AccountID: id.String()}, nil
}
