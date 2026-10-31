package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/payment/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

const (
	recoverInterval = time.Minute
	recoverBatch    = 50
	// stuckAfter: un pago en processing más viejo que esto ya no tiene un
	// request esperando al PSP (su timeout es menor): quedó trabado.
	stuckAfter = 2 * time.Minute
	// notFoundAfter: si el PSP sigue sin registro de la operación pasado este
	// plazo, nunca la recibió (p. ej. timeout antes de llegar) → failed.
	notFoundAfter = 30 * time.Minute
)

// ProcessingRecoverer resuelve los pagos trabados en processing: el PSP no
// confirmó el resultado (timeout, red) o capturó pero falló la persistencia, o
// el proceso cayó en el medio. Le pregunta al PSP por la clave de idempotencia
// y registra lo que pasó. Corre en cmd/worker.
type ProcessingRecoverer struct {
	repo      PaymentRepository
	router    PSPRouter
	txManager TxManager
	publisher EventPublisher
	clock     Clock
	logger    *slog.Logger
}

func NewProcessingRecoverer(
	repo PaymentRepository,
	router PSPRouter,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
	logger *slog.Logger,
) *ProcessingRecoverer {
	return &ProcessingRecoverer{repo: repo, router: router, txManager: txManager,
		publisher: publisher, clock: clock, logger: logger}
}

// Start corre un ciclo al arrancar y después cada recoverInterval, hasta que
// ctx se cancele.
func (r *ProcessingRecoverer) Start(ctx context.Context) error {
	ticker := time.NewTicker(recoverInterval)
	defer ticker.Stop()
	r.logger.Info("payment processing recoverer started", "interval", recoverInterval)

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

func (r *ProcessingRecoverer) runCycle(ctx context.Context) {
	now := r.clock.Now()
	stuck, err := r.repo.ListStuckProcessing(ctx, now.Add(-stuckAfter), recoverBatch)
	if err != nil {
		r.logger.Error("payment recoverer: list stuck payments", "error", err)
		return
	}
	for _, p := range stuck {
		if err := r.recover(ctx, p, now); err != nil {
			r.logger.Error("payment recoverer: recover payment", "payment_id", p.ID(), "error", err)
		}
	}
}

// recover consulta al PSP y lleva el pago a captured o failed. Si el PSP no
// responde, o todavía no tiene registro y no pasó notFoundAfter, lo deja para
// el próximo ciclo.
func (r *ProcessingRecoverer) recover(ctx context.Context, p *domain.Payment, now time.Time) error {
	psp, err := r.router.Route(ctx, p.Method(), p.TenantID())
	if err != nil {
		return fmt.Errorf("route psp: %w", err)
	}
	lookup, err := psp.GetCapture(ctx, PSPCaptureLookupRequest{
		PaymentID:      p.ID().String(),
		IdempotencyKey: p.IdempotencyKey(),
		Amount:         p.Amount().Amount(),
		Currency:       p.Amount().Currency(),
	})
	if err != nil {
		return fmt.Errorf("psp lookup: %w", err)
	}

	switch {
	case lookup.Found && lookup.Approved:
		pspFee := domain.ReconstituteMoney(lookup.PSPFee, p.Amount().Currency())
		if err := p.Capture(psp.Name(), lookup.PSPReference, p.PlatformFee(), pspFee); err != nil {
			return err
		}
	case lookup.Found:
		if err := p.Fail(psp.Name(), "declined by the payment provider"); err != nil {
			return err
		}
	case now.Sub(p.UpdatedAt()) >= notFoundAfter:
		if err := p.Fail(psp.Name(), "the payment provider has no record of the payment"); err != nil {
			return err
		}
	default:
		return nil // el PSP todavía puede reflejarla: próximo ciclo
	}

	// ListStuckProcessing es cross-tenant; la escritura corre con el tenant
	// del pago (RLS).
	ctx = pkgpostgres.WithTenantID(ctx, p.TenantID().String())
	err = r.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := r.repo.Update(ctx, p, domain.StatusProcessing); err != nil {
			return err
		}
		return r.publisher.Publish(ctx, p.PullEvents()...)
	})
	if errors.Is(err, domain.ErrPaymentChanged) {
		return nil // el request original lo resolvió mientras tanto
	}
	if err != nil {
		return fmt.Errorf("persist recovered payment: %w", err)
	}
	r.logger.Info("payment recoverer: resolved stuck payment",
		"payment_id", p.ID(), "status", p.Status())
	return nil
}
