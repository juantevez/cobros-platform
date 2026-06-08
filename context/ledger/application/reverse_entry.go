package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// ReverseEntryUseCase crea un asiento que anula contablemente a uno existente.
//
// La reversa invierte cada posting (débitos↔créditos) con el mismo monto,
// dejando el efecto neto en cero. El asiento original nunca se modifica:
// append-only garantizado.
//
// Idempotencia: la clave del reverso es "reverse_<original_idempotency_key>".
// Si ya existe, se retorna el existente sin crear uno nuevo.
type ReverseEntryUseCase struct {
	entryRepo   EntryRepository
	balanceRepo BalanceRepository
	txManager   TxManager
	publisher   EventPublisher
}

func NewReverseEntryUseCase(
	entryRepo EntryRepository,
	balanceRepo BalanceRepository,
	txManager TxManager,
	publisher EventPublisher,
) *ReverseEntryUseCase {
	return &ReverseEntryUseCase{
		entryRepo:   entryRepo,
		balanceRepo: balanceRepo,
		txManager:   txManager,
		publisher:   publisher,
	}
}

func (uc *ReverseEntryUseCase) Execute(ctx context.Context, cmd ReverseEntryCmd) (ReverseEntryResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return ReverseEntryResult{}, err
	}

	originalID, err := domain.ParseEntryID(cmd.OriginalEntryID)
	if err != nil {
		return ReverseEntryResult{}, err
	}

	// ── Cargar el asiento original ────────────────────────────────────────────

	original, err := uc.entryRepo.FindByID(ctx, originalID)
	if err != nil {
		return ReverseEntryResult{}, fmt.Errorf("find original entry: %w", err)
	}

	// Verificar aislamiento: el entry pertenece al tenant del caller.
	if original.TenantID() != tenantID {
		return ReverseEntryResult{}, domain.ErrEntryNotFound
	}

	// ── Idempotencia del reverso ──────────────────────────────────────────────

	reverseKey := "reverse_" + original.IdempotencyKey()
	existing, err := uc.entryRepo.FindByIdempotencyKey(ctx, tenantID, reverseKey)
	if err == nil && existing != nil {
		return ReverseEntryResult{ReverseEntryID: existing.ID().String()}, nil
	}

	// ── Construir el asiento de reversa ───────────────────────────────────────

	reverseID := domain.NewEntryID()
	reverse, err := original.BuildReverse(reverseID, reverseKey)
	if err != nil {
		return ReverseEntryResult{}, fmt.Errorf("build reverse: %w", err)
	}

	// ── Persistir en una sola transacción ─────────────────────────────────────

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.entryRepo.Save(ctx, reverse); err != nil {
			return fmt.Errorf("save reverse entry: %w", err)
		}
		if err := uc.balanceRepo.Apply(ctx, reverse.Postings()); err != nil {
			return fmt.Errorf("apply reverse balances: %w", err)
		}
		return uc.publisher.Publish(ctx, reverse.PullEvents()...)
	}); err != nil {
		return ReverseEntryResult{}, err
	}

	return ReverseEntryResult{ReverseEntryID: reverseID.String()}, nil
}
