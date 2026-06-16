package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

const retryBatchSize = 50

// RetryDeliveryUseCase despacha una delivery hacia el endpoint del comercio.
// Se usa tanto para el reintento automático (RetryPoller) como para el
// reintento manual solicitado por el operador vía HTTP.
type RetryDeliveryUseCase struct {
	endpointRepo EndpointRepository
	deliveryRepo DeliveryRepository
	dispatcher   HTTPDispatcher
	publisher    EventPublisher
	clock        Clock
}

func NewRetryDeliveryUseCase(
	endpointRepo EndpointRepository,
	deliveryRepo DeliveryRepository,
	dispatcher HTTPDispatcher,
	publisher EventPublisher,
	clock Clock,
) *RetryDeliveryUseCase {
	return &RetryDeliveryUseCase{
		endpointRepo: endpointRepo,
		deliveryRepo: deliveryRepo,
		dispatcher:   dispatcher,
		publisher:    publisher,
		clock:        clock,
	}
}

// Execute despacha una delivery específica (reintento manual).
func (uc *RetryDeliveryUseCase) Execute(ctx context.Context, cmd RetryDeliveryCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	deliveryID, err := domain.ParseDeliveryID(cmd.DeliveryID)
	if err != nil {
		return err
	}

	delivery, err := uc.deliveryRepo.FindByID(ctx, deliveryID)
	if err != nil {
		return err
	}
	if delivery.TenantID() != tenantID {
		return domain.ErrDeliveryNotFound
	}
	if delivery.Status().IsFinal() {
		return domain.ErrDeliveryNotRetryable
	}

	return uc.dispatch(ctx, delivery)
}

// dispatch ejecuta el HTTP call y actualiza el estado de la delivery.
func (uc *RetryDeliveryUseCase) dispatch(ctx context.Context, delivery *domain.WebhookDelivery) error {
	endpoint, err := uc.endpointRepo.FindByID(ctx, delivery.EndpointID())
	if err != nil {
		return fmt.Errorf("load endpoint %s: %w", delivery.EndpointID(), err)
	}

	attempt, _ := uc.dispatcher.Dispatch(ctx, endpoint, delivery)

	now := uc.clock.Now()
	_ = delivery.RecordAttempt(attempt, now)

	if err := uc.deliveryRepo.Update(ctx, delivery); err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}

	// Publicar DeliveryExhaustedEvent si se agotaron los reintentos.
	if evs := delivery.PullEvents(); len(evs) > 0 {
		_ = uc.publisher.Publish(ctx, evs...)
	}

	return nil
}

// ── RetryPoller ───────────────────────────────────────────────────────────────

// RetryPoller es un proceso de fondo que despacha las deliveries pendientes.
// Corre en cmd/worker como una goroutine independiente.
//
// Intervalo de polling: cada 5s. Por cada ciclo:
//  1. Busca hasta 50 deliveries con nextRetryAt <= now
//  2. Por cada una, carga el endpoint y ejecuta el HTTP call
//  3. Actualiza el estado (delivered / failed / exhausted)
type RetryPoller struct {
	retryUC  *RetryDeliveryUseCase
	repo     DeliveryRepository
	interval time.Duration
	logger   *slog.Logger
}

func NewRetryPoller(
	retryUC *RetryDeliveryUseCase,
	repo DeliveryRepository,
	interval time.Duration,
	logger *slog.Logger,
) *RetryPoller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &RetryPoller{retryUC: retryUC, repo: repo, interval: interval, logger: logger}
}

// Start arranca el poller. Bloquea hasta que ctx sea cancelado.
func (p *RetryPoller) Start(ctx context.Context) error {
	p.logger.Info("retry poller started", "interval", p.interval)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("retry poller stopped")
			return nil
		case <-ticker.C:
			p.runCycle(ctx)
		}
	}
}

func (p *RetryPoller) runCycle(ctx context.Context) {
	deliveries, err := p.repo.ListDueForRetry(ctx, time.Now().UTC(), retryBatchSize)
	if err != nil {
		p.logger.Error("retry poller: list due deliveries", "error", err)
		return
	}
	if len(deliveries) == 0 {
		return
	}

	p.logger.Info("retry poller: dispatching", "count", len(deliveries))
	for _, d := range deliveries {
		if err := p.retryUC.dispatch(ctx, d); err != nil {
			p.logger.Error("retry poller: dispatch failed",
				"delivery_id", d.ID(),
				"event_type", d.EventType(),
				"error", err,
			)
		}
	}
}
