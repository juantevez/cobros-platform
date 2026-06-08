package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// PostEntryUseCase registra un asiento de doble partida en el libro mayor.
//
// Es el caso de uso más crítico del sistema: mueve dinero entre cuentas
// de forma atómica, idempotente y auditada.
//
// Flujo:
//  1. Validar y parsear el comando.
//  2. Verificar idempotencia: si ya existe un entry con esa clave, retornarlo.
//  3. Crear el JournalEntry en el dominio (valida doble partida).
//  4. En una sola transacción:
//     a. Persistir el entry y sus postings.
//     b. Actualizar los saldos de cada cuenta afectada.
//     c. Guardar el evento en el outbox.
//
// Garantías:
//   - Un mismo (tenantID, idempotencyKey) produce exactamente un asiento.
//   - El saldo de las cuentas siempre refleja los postings persistidos.
//   - El evento EntryPosted solo se publica si el asiento fue persistido.
type PostEntryUseCase struct {
	entryRepo   EntryRepository
	balanceRepo BalanceRepository
	txManager   TxManager
	publisher   EventPublisher
	clock       Clock
}

func NewPostEntryUseCase(
	entryRepo EntryRepository,
	balanceRepo BalanceRepository,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *PostEntryUseCase {
	return &PostEntryUseCase{
		entryRepo:   entryRepo,
		balanceRepo: balanceRepo,
		txManager:   txManager,
		publisher:   publisher,
		clock:       clock,
	}
}

func (uc *PostEntryUseCase) Execute(ctx context.Context, cmd PostEntryCmd) (PostEntryResult, error) {
	// ── 1. Validar inputs ────────────────────────────────────────────────────

	if cmd.IdempotencyKey == "" {
		return PostEntryResult{}, fmt.Errorf("idempotency key is required")
	}
	if len(cmd.Lines) < 2 {
		return PostEntryResult{}, domain.ErrNotEnoughPostings
	}

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return PostEntryResult{}, err
	}

	occurredAt := cmd.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = uc.clock.Now()
	}

	// ── 2. Idempotencia ──────────────────────────────────────────────────────
	// Si ya existe un entry con esta clave, retornarlo sin crear uno nuevo.

	existing, err := uc.entryRepo.FindByIdempotencyKey(ctx, tenantID, cmd.IdempotencyKey)
	if err != nil && !errors.Is(err, domain.ErrEntryNotFound) {
		return PostEntryResult{}, fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil {
		return PostEntryResult{
			EntryID:     existing.ID().String(),
			CreatedAt:   existing.CreatedAt(),
			WasExisting: true,
		}, nil
	}

	// ── 3. Construir el agregado (valida doble partida en el dominio) ─────────

	inputs := make([]domain.PostingInput, len(cmd.Lines))
	for i, line := range cmd.Lines {
		accountID, parseErr := domain.ParseAccountID(line.AccountID)
		if parseErr != nil {
			return PostEntryResult{}, fmt.Errorf("line %d: %w", i, parseErr)
		}
		direction, parseErr := domain.ParseDirection(line.Direction)
		if parseErr != nil {
			return PostEntryResult{}, fmt.Errorf("line %d: %w", i, parseErr)
		}
		inputs[i] = domain.PostingInput{
			AccountID: accountID,
			Direction: direction,
			Amount:    line.Amount,
			Currency:  line.Currency,
		}
	}

	entryID := domain.NewEntryID()
	entry, err := domain.NewJournalEntry(
		entryID, tenantID,
		cmd.IdempotencyKey,
		cmd.Description,
		cmd.Metadata,
		occurredAt,
		inputs,
	)
	if err != nil {
		// ErrEntryNotBalanced, ErrCurrencyMismatch, etc. → 422 en el handler
		return PostEntryResult{}, err
	}

	// ── 4. Persistir en una sola transacción ─────────────────────────────────

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		// a. Persistir entry + postings
		if err := uc.entryRepo.Save(ctx, entry); err != nil {
			return fmt.Errorf("save entry: %w", err)
		}

		// b. Actualizar saldos de cuentas afectadas
		if err := uc.balanceRepo.Apply(ctx, entry.Postings()); err != nil {
			return fmt.Errorf("apply balances: %w", err)
		}

		// c. Guardar evento en outbox (misma tx)
		return uc.publisher.Publish(ctx, entry.PullEvents()...)
	}); err != nil {
		return PostEntryResult{}, err
	}

	return PostEntryResult{
		EntryID:     entryID.String(),
		CreatedAt:   uc.clock.Now(),
		WasExisting: false,
	}, nil
}

// ── GetBalance ────────────────────────────────────────────────────────────────

// GetBalanceUseCase consulta el saldo de una cuenta contable.
type GetBalanceUseCase struct {
	accountRepo AccountRepository
	balanceRepo BalanceRepository
}

func NewGetBalanceUseCase(accountRepo AccountRepository, balanceRepo BalanceRepository) *GetBalanceUseCase {
	return &GetBalanceUseCase{accountRepo: accountRepo, balanceRepo: balanceRepo}
}

func (uc *GetBalanceUseCase) Execute(ctx context.Context, q GetBalanceQuery) (GetBalanceResult, error) {
	accountID, err := domain.ParseAccountID(q.AccountID)
	if err != nil {
		return GetBalanceResult{}, err
	}

	account, err := uc.accountRepo.FindByID(ctx, accountID)
	if err != nil {
		return GetBalanceResult{}, err
	}

	balance, err := uc.balanceRepo.GetBalance(ctx, accountID)
	if err != nil {
		return GetBalanceResult{}, fmt.Errorf("get balance: %w", err)
	}

	return GetBalanceResult{
		AccountID: accountID.String(),
		Balance:   balance,
		Currency:  account.Currency(),
	}, nil
}

// realClock para tests y wiring.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// RealClock retorna una implementación de Clock con time.Now().
func RealClock() Clock { return realClock{} }
