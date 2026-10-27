package application

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// InitiatePayoutUseCase calcula el monto disponible y ejecuta el desembolso.
//
// Flujo:
//  1. Validar la clave de idempotencia (obligatoria).
//  2. Obtener la cuenta bancaria VERIFICADA del comercio (Onboarding).
//  3. RunInTx { pg_advisory_xact_lock(tenant,currency) + saldo en Ledger
//     + Create Payout + Save + Publish PayoutInitiatedEvent }.
//     El lock serializa payouts concurrentes del mismo tenant/moneda y cierra
//     la ventana TOCTOU entre leer el saldo y reservar los fondos (§10.4).
//     La búsqueda por (tenant, idempotency key) dentro de la misma tx hace
//     replay de retries; el UNIQUE de la columna es la red de seguridad (§10.2).
//  4. MarkProcessing + ejecutar transferencia bancaria.
//  5. Si OK        → Confirm → RunInTx { Update + Publish PayoutConfirmedEvent }.
//     Si rechazo  → Fail    → RunInTx { Update + Publish PayoutFailedEvent }
//     (el Ledger revierte merchant_balance ← payout_transit).
//     Si timeout/error de red → MarkUnknown → RunInTx { Update + Publish
//     PayoutUnknownEvent }: NO se revierte nada; los fondos quedan en
//     payout_transit hasta que reconciliación resuelva (§10.3).
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

	// ── 1. Idempotencia de negocio ────────────────────────────────────────────
	// Sin clave no hay forma de distinguir un retry de un segundo desembolso
	// legítimo: se rechaza el request (400 en la capa HTTP).
	key := cmd.IdempotencyKey
	if key == "" {
		return InitiatePayoutResult{}, domain.ErrIdempotencyKeyRequired
	}

	currency := cmd.Currency
	if currency == "" {
		currency = "ARS" // default
	}

	// ── 2. Obtener cuenta bancaria verificada ─────────────────────────────────

	bankAccount, err := uc.bankProvider.GetBankAccount(ctx, tenantID)
	if err != nil {
		return InitiatePayoutResult{}, fmt.Errorf("get bank account: %w", err)
	}
	if bankAccount.AccountNumber() == "" {
		return InitiatePayoutResult{}, domain.ErrNoBankAccount
	}

	// ── 3. Tx única: lock contable + saldo + creación + publicación ───────────
	// Todo dentro de la misma transacción para que el advisory lock cubra
	// también el INSERT (reserva de fondos). Un rollback deja el estado sin
	// tocar; un commit publica el asiento de initiated de forma atómica.

	var payout *domain.Payout
	var replayed bool

	err = uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		// 3a. Serializar payouts concurrentes del mismo tenant/moneda.
		// Implementaciones del lock fuera de Postgres (tests, in-memory) son
		// no-op: la uniquedad de la clave sigue dando idempotencia.
		if locker, ok := uc.balanceChecker.(BalanceLocker); ok {
			if err := locker.LockBalance(ctx, tenantID, currency); err != nil {
				return fmt.Errorf("lock balance: %w", err)
			}
		}

		// 3b. Replay: ¿existe ya un payout con esta clave?
		existing, err := uc.repo.FindByIdempotencyKey(ctx, tenantID, key)
		switch {
		case err == nil:
			if !existing.MatchesRequest(cmd.Amount, currency) {
				return domain.ErrIdempotencyKeyConflict
			}
			payout = existing
			replayed = true
			return nil
		case !errors.Is(err, domain.ErrPayoutNotFound):
			return fmt.Errorf("lookup idempotency key: %w", err)
		}

		// 3c. Verificar saldo disponible (con el lock tomado).
		available, err := uc.balanceChecker.GetAvailableBalance(ctx, tenantID, currency)
		if err != nil {
			return fmt.Errorf("get available balance: %w", err)
		}

		// Determinar monto: si no se especifica, usar el saldo completo.
		payoutAmount := cmd.Amount
		if payoutAmount == 0 {
			payoutAmount = available
		}

		if payoutAmount <= 0 {
			return domain.ErrInsufficientBalance
		}
		if payoutAmount > available {
			return domain.ErrInsufficientBalance
		}

		money, err := domain.NewMoney(payoutAmount, currency)
		if err != nil {
			return err
		}

		// 3d. Crear el Payout y persistir + publicar PayoutInitiatedEvent.
		// El Ledger consumer registra el movimiento: merchant_balance → payout_transit.
		id := domain.NewPayoutID()
		p, err := domain.NewPayout(id, tenantID, money, bankAccount, key)
		if err != nil {
			return err
		}
		if err := uc.repo.Save(ctx, p); err != nil {
			// Carrera contra otro request con la misma clave (ambos pasaron 3b):
			// el UNIQUE falla aquí. No podemos retornar el payout de la otra tx
			// (aún no commiteada), así que propagamos ErrPayoutAlreadyExists
			// y el caller debe reintentar — el retry encontrará el registro.
			return fmt.Errorf("save payout: %w", err)
		}
		if err := uc.publisher.Publish(ctx, p.PullEvents()...); err != nil {
			return err
		}
		payout = p
		return nil
	})
	if err != nil {
		return InitiatePayoutResult{}, err
	}

	if replayed {
		return resultOf(payout, true), nil
	}

	// ── 4. Marcar como processing y ejecutar transferencia ────────────────────

	if err := payout.MarkProcessing(); err != nil {
		return InitiatePayoutResult{}, err
	}
	if err := uc.repo.Update(ctx, payout); err != nil {
		return InitiatePayoutResult{}, fmt.Errorf("update to processing: %w", err)
	}

	transferResult, transferErr := uc.transferAdapter.Transfer(ctx, TransferRequest{
		PayoutID:       payout.ID().String(),
		IdempotencyKey: "transfer_" + payout.ID().String(),
		Amount:         payout.Amount().Amount(),
		Currency:       payout.Amount().Currency(),
		AccountType:    bankAccount.AccountType,
		AccountNumber:  bankAccount.AccountNumber(),
		BankName:       bankAccount.BankName,
		HolderName:     bankAccount.HolderName,
		Description:    fmt.Sprintf("Desembolso %s", payout.ID().String()[:8]),
	})

	// ── 5. Resultado de la transferencia ──────────────────────────────────────

	if transferErr != nil {
		// Timeout / cancelación / error de red: resultado INDETERMINADO.
		// Fallarlo sería peligroso: el banco puede haber acreditado los fondos
		// y la reversa del asiento dejaría al comercio pagando un dinero que
		// efectivamente recibió. Se marca unknown y se delega a reconciliación.
		if isIndeterminate(transferErr) {
			if err := payout.MarkUnknown(transferErr.Error()); err != nil {
				log.Printf("payout %s: mark unknown: %v", payout.ID(), err)
			}
			_ = uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
				_ = uc.repo.Update(ctx, payout)
				return uc.publisher.Publish(ctx, payout.PullEvents()...)
			})
			return resultOf(payout, false),
				fmt.Errorf("transfer result unknown, awaiting reconciliation: %w", transferErr)
		}

		// Rechazo conocido del banco (cuenta inválida, titular distinto, etc.):
		// fallo definitivo → el Ledger revierte el asiento.
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

	return resultOf(payout, false), nil
}

func resultOf(p *domain.Payout, replayed bool) InitiatePayoutResult {
	return InitiatePayoutResult{
		PayoutID:      p.ID().String(),
		Amount:        p.Amount().Amount(),
		Currency:      p.Amount().Currency(),
		Status:        p.Status().String(),
		BankReference: p.BankReference(),
		Replayed:      replayed,
	}
}
