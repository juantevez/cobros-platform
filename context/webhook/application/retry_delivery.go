package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

const (
	retryBatchSize = 50
	// retryConcurrency limita los despachos HTTP simultáneos por ciclo: un
	// endpoint lento (timeout de 30s) no frena al resto del batch.
	retryConcurrency = 10
	// claimLease es cuánto dura el reclamo de una delivery: tiene que cubrir
	// el POST (timeout de 30s) y la escritura del resultado. Si el proceso
	// muere a mitad del envío, la delivery vuelve a estar disponible al vencer.
	claimLease = 2 * time.Minute
)

// RetryDeliveryUseCase despacha una delivery hacia el endpoint del comercio.
// Se usa tanto para el reintento automático (RetryPoller) como para el
// reintento manual solicitado por el operador vía HTTP.
type RetryDeliveryUseCase struct {
	endpointRepo EndpointRepository
	deliveryRepo DeliveryRepository
	dispatcher   HTTPDispatcher
	txManager    TxManager
	publisher    EventPublisher
	clock        Clock
}

func NewRetryDeliveryUseCase(
	endpointRepo EndpointRepository,
	deliveryRepo DeliveryRepository,
	dispatcher HTTPDispatcher,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *RetryDeliveryUseCase {
	return &RetryDeliveryUseCase{
		endpointRepo: endpointRepo,
		deliveryRepo: deliveryRepo,
		dispatcher:   dispatcher,
		txManager:    txManager,
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
	// RLS: todo el despacho (endpoint, reclamo, tx del resultado) corre con
	// el tenant de la delivery. El poller llega sin tenant: lista como
	// operador de plataforma, cross-tenant.
	ctx = pkgpostgres.WithTenantID(ctx, delivery.TenantID().String())

	endpoint, err := uc.endpointRepo.FindByID(ctx, delivery.EndpointID())
	if err != nil {
		return fmt.Errorf("load endpoint %s: %w", delivery.EndpointID(), err)
	}
	// Un endpoint desactivado no recibe más entregas, ni siquiera las que ya
	// estaban en cola. No se registra intento: la delivery queda como estaba.
	if !endpoint.Active() {
		return domain.ErrEndpointInactive
	}

	// Reclamo antes del POST: el poller, el reintento manual y otra réplica
	// del worker pueden tener la misma delivery; solo uno la despacha.
	prevAttempts := delivery.AttemptCount()
	now := uc.clock.Now()
	claimed, err := uc.deliveryRepo.Claim(ctx, delivery.ID(), prevAttempts, now, now.Add(claimLease))
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		return domain.ErrDeliveryInProgress
	}

	attempt, _ := uc.dispatcher.Dispatch(ctx, endpoint, delivery)

	_ = delivery.RecordAttempt(attempt, uc.clock.Now())

	// Estado, intento y DeliveryExhaustedEvent se guardan juntos: si algo
	// falla no queda una delivery agotada sin su evento ni un estado sin el
	// intento que lo explica. La delivery sigue como estaba en la base y el
	// poller la vuelve a despachar (at-least-once).
	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.deliveryRepo.Update(ctx, delivery, prevAttempts); err != nil {
			return fmt.Errorf("update delivery: %w", err)
		}
		if evs := delivery.PullEvents(); len(evs) > 0 {
			if err := uc.publisher.Publish(ctx, evs...); err != nil {
				return fmt.Errorf("publish delivery events: %w", err)
			}
		}
		return nil
	})
}

// ── RetryPoller ───────────────────────────────────────────────────────────────

// RetryPoller es un proceso de fondo que despacha las deliveries pendientes.
// Corre en cmd/worker como una goroutine independiente.
//
// Intervalo de polling: cada 5s. Por cada ciclo:
//  1. Busca hasta 50 deliveries con nextRetryAt <= now
//  2. Las despacha con hasta 10 HTTP calls en paralelo
//  3. Actualiza el estado (delivered / failed / exhausted)
type RetryPoller struct {
	retryUC     *RetryDeliveryUseCase
	repo        DeliveryRepository
	interval    time.Duration
	concurrency int
	logger      *slog.Logger
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
	return &RetryPoller{retryUC: retryUC, repo: repo, interval: interval, concurrency: retryConcurrency, logger: logger}
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
	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, d := range deliveries {
		wg.Add(1)
		sem <- struct{}{}
		go func(d *domain.WebhookDelivery) {
			defer wg.Done()
			defer func() { <-sem }()
			err := p.retryUC.dispatch(ctx, d)
			if errors.Is(err, domain.ErrDeliveryInProgress) {
				return // la despacha otro proceso
			}
			if err != nil {
				p.logger.Error("retry poller: dispatch failed",
					"delivery_id", d.ID(),
					"event_type", d.EventType(),
					"error", err,
				)
			}
		}(d)
	}
	wg.Wait()
}
