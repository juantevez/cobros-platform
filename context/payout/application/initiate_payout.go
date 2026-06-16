package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// InitiatePayoutUseCase calcula el monto disponible y ejecuta el desembolso.
//
// Flujo:
//  1. Obtener la cuenta bancaria del comercio (Onboarding).
//  2. Consultar saldo disponible en Ledger (BalanceChecker).
//  3. Determinar el monto: el solicitado o el saldo completo si amount=0.
//  4. Crear el Payout → emite PayoutInitiatedEvent.
//  5. RunInTx { Save payout + Publish PayoutInitiatedEvent }.
//     → El Ledger consumer registra: merchant_balance CREDIT, payout_transit DEBIT.
//  6. MarkProcessing + ejecutar transferencia bancaria.
//  7. Si OK → Confirm → RunInTx { Update + Publish PayoutConfirmedEvent }.
//  8. Si falla → Fail → RunInTx { Update + Publish PayoutFailedEvent }.
//
// Nota: el Ledger actúa sobre los eventos de forma asíncrona (consistencia eventual).
// El payout no espera confirmación del Ledger para continuar.
type InitiatePayoutUseCase struct {
	repo            PayoutRepository
	balanceChecker  BalanceChecker
	bankProvider    BankAccountProvider
	transferAdapter BankTransferAdapter
	txManager       TxManager
	publisher       EventPublisher
}

func NewInitiatePayoutUseCase(
	repo PayoutRepository,
	balanceChecker BalanceChecker,
	bankProvider BankAccountProvider,
	transferAdapter BankTransferAdapter,
	txManager TxManager,
	publisher EventPublisher,
) *InitiatePayoutUseCase {
	return &InitiatePayoutUseCase{
		repo:            repo,
		balanceChecker:  balanceChecker,
		bankProvider:    bankProvider,
		transferAdapter: transferAdapter,
		txManager:       txManager,
		publisher:       publisher,
	}
}

func (uc *InitiatePayoutUseCase) Execute(ctx context.Context, cmd InitiatePayoutCmd) (InitiatePayoutResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return InitiatePayoutResult{}, err
	}

	currency := cmd.Currency
	if currency == "" {
		currency = "ARS" // default
	}

	// ── 1. Obtener cuenta bancaria ────────────────────────────────────────────

	bankAccount, err := uc.bankProvider.GetBankAccount(ctx, tenantID)
	if err != nil {
		return InitiatePayoutResult{}, fmt.Errorf("get bank account: %w", err)
	}
	if bankAccount.AccountNumber == "" {
		return InitiatePayoutResult{}, domain.ErrNoBankAccount
	}

	// ── 2. Verificar saldo disponible ─────────────────────────────────────────

	available, err := uc.balanceChecker.GetAvailableBalance(ctx, tenantID, currency)
	if err != nil {
		return InitiatePayoutResult{}, fmt.Errorf("get available balance: %w", err)
	}

	// Determinar monto: si no se especifica, usar el saldo completo.
	payoutAmount := cmd.Amount
	if payoutAmount == 0 {
		payoutAmount = available
	}

	if payoutAmount <= 0 {
		return InitiatePayoutResult{}, domain.ErrInsufficientBalance
	}
	if payoutAmount > available {
		return InitiatePayoutResult{}, domain.ErrInsufficientBalance
	}

	money, err := domain.NewMoney(payoutAmount, currency)
	if err != nil {
		return InitiatePayoutResult{}, err
	}

	// ── 3. Crear el Payout ────────────────────────────────────────────────────

	id := domain.NewPayoutID()
	payout, err := domain.NewPayout(id, tenantID, money, bankAccount)
	if err != nil {
		return InitiatePayoutResult{}, err
	}

	// ── 4. Persistir + publicar PayoutInitiatedEvent ──────────────────────────
	// El Ledger consumer registra el movimiento: merchant_balance → payout_transit

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Save(ctx, payout); err != nil {
			return fmt.Errorf("save payout: %w", err)
		}
		return uc.publisher.Publish(ctx, payout.PullEvents()...)
	}); err != nil {
		return InitiatePayoutResult{}, err
	}

	// ── 5. Marcar como processing y ejecutar transferencia ────────────────────

	if err := payout.MarkProcessing(); err != nil {
		return InitiatePayoutResult{}, err
	}
	if err := uc.repo.Update(ctx, payout); err != nil {
		return InitiatePayoutResult{}, fmt.Errorf("update to processing: %w", err)
	}

	transferResult, transferErr := uc.transferAdapter.Transfer(ctx, TransferRequest{
		PayoutID:       id.String(),
		IdempotencyKey: "transfer_" + id.String(),
		Amount:         payoutAmount,
		Currency:       currency,
		AccountType:    bankAccount.AccountType,
		AccountNumber:  bankAccount.AccountNumber,
		BankName:       bankAccount.BankName,
		HolderName:     bankAccount.HolderName,
		Description:    fmt.Sprintf("Desembolso %s", id.String()[:8]),
	})

	// ── 6. Resultado de la transferencia ──────────────────────────────────────

	if transferErr != nil {
		_ = payout.Fail(transferErr.Error())
		_ = uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			_ = uc.repo.Update(ctx, payout)
			return uc.publisher.Publish(ctx, payout.PullEvents()...)
		})
		return InitiatePayoutResult{}, fmt.Errorf("transfer failed: %w", transferErr)
	}

	if err := payout.Confirm(transferResult.BankReference); err != nil {
		return InitiatePayoutResult{}, err
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, payout); err != nil {
			return fmt.Errorf("update confirmed: %w", err)
		}
		return uc.publisher.Publish(ctx, payout.PullEvents()...)
	}); err != nil {
		return InitiatePayoutResult{}, err
	}

	return InitiatePayoutResult{
		PayoutID:      id.String(),
		Amount:        payoutAmount,
		Currency:      currency,
		Status:        payout.Status().String(),
		BankReference: transferResult.BankReference,
	}, nil
}
