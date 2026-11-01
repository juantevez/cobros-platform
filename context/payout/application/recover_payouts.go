package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

const (
	recoverInterval = time.Minute
	recoverBatch    = 50
	// stuckAfter: un payout en processing más viejo que esto ya no tiene un
	// request esperando al banco: quedó trabado (proceso caído o persistencia
	// fallida). Los unknown se consultan con el mismo corte.
	stuckAfter = 2 * time.Minute
	// notFoundAfter: si el banco sigue sin registro de la transferencia pasado
	// este plazo, nunca la recibió → failed (el Ledger revierte el asiento).
	notFoundAfter = 30 * time.Minute
)

// PayoutRecoverer resuelve los payouts que quedaron en unknown (el banco no
// confirmó el resultado) o trabados en processing (el proceso cayó o falló la
// persistencia): le pregunta al banco por la clave de idempotencia de la
// transferencia y los confirma o falla. Corre en cmd/worker.
type PayoutRecoverer struct {
	repo      PayoutRepository
	transfer  BankTransferAdapter
	txManager TxManager
	publisher EventPublisher
	clock     Clock
	logger    *slog.Logger
}

func NewPayoutRecoverer(
	repo PayoutRepository,
	transfer BankTransferAdapter,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
	logger *slog.Logger,
) *PayoutRecoverer {
	return &PayoutRecoverer{repo: repo, transfer: transfer, txManager: txManager,
		publisher: publisher, clock: clock, logger: logger}
}

// Start corre un ciclo al arrancar y después cada recoverInterval, hasta que
// ctx se cancele.
func (r *PayoutRecoverer) Start(ctx context.Context) error {
	ticker := time.NewTicker(recoverInterval)
	defer ticker.Stop()
	r.logger.Info("payout recoverer started", "interval", recoverInterval)

	if ctx.Err() == nil {
		r.runCycle(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.runCycle(ctx)
		}
	}
}

func (r *PayoutRecoverer) runCycle(ctx context.Context) {
	now := r.clock.Now()
	pending, err := r.repo.ListUnresolved(ctx, now.Add(-stuckAfter), recoverBatch)
	if err != nil {
		r.logger.Error("payout recoverer: list unresolved payouts", "error", err)
		return
	}
	for _, p := range pending {
		if err := r.recover(ctx, p, now); err != nil {
			r.logger.Error("payout recoverer: recover payout", "payout_id", p.ID(), "error", err)
		}
	}
}

// recover consulta al banco y lleva el payout a confirmed o failed. Si el
// banco no responde, o todavía no tiene registro y no pasó notFoundAfter, lo
// deja para el próximo ciclo.
func (r *PayoutRecoverer) recover(ctx context.Context, p *domain.Payout, now time.Time) error {
	lookup, err := r.transfer.GetTransfer(ctx, TransferLookupRequest{
		PayoutID:       p.ID().String(),
		IdempotencyKey: transferIdempotencyKey(p),
		Amount:         p.Amount().Amount(),
		Currency:       p.Amount().Currency(),
	})
	if err != nil {
		return fmt.Errorf("bank lookup: %w", err)
	}

	from := p.Status()
	switch {
	case lookup.Found && lookup.Completed:
		err = p.Confirm(lookup.BankReference)
	case lookup.Found:
		err = p.Fail(domain.FailureRejectedByBank, "")
	case now.Sub(p.UpdatedAt()) >= notFoundAfter:
		err = p.Fail("the bank has no record of the transfer", "")
	default:
		return nil // el banco todavía puede reflejarla: próximo ciclo
	}
	if err != nil {
		return err
	}

	// ListUnresolved es cross-tenant; la escritura corre con el tenant del
	// payout (RLS).
	ctx = pkgpostgres.WithTenantID(ctx, p.TenantID().String())
	err = r.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := r.repo.Update(ctx, p, from); err != nil {
			return err
		}
		return r.publisher.Publish(ctx, p.PullEvents()...)
	})
	if errors.Is(err, domain.ErrPayoutChanged) {
		return nil // el request original lo resolvió mientras tanto
	}
	if err != nil {
		return fmt.Errorf("persist recovered payout: %w", err)
	}
	r.logger.Info("payout recoverer: resolved payout", "payout_id", p.ID(), "status", p.Status())
	return nil
}
