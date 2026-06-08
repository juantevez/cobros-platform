package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

const (
	defaultInterval  = 1 * time.Second
	defaultBatchSize = 50
)

// Relay lee mensajes pendientes del Store y los publica en el bus de eventos.
// Se ejecuta como proceso de fondo en cmd/worker.
//
// La combinación de SKIP LOCKED (en FetchPending) + Nats-Msg-Id (dedup en
// JetStream) hace al Relay seguro para ejecutar en múltiples instancias.
type Relay struct {
	store     Store
	publisher eventbus.Publisher
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
}

// Option configura el Relay.
type Option func(*Relay)

// WithInterval configura el período entre ciclos de publicación.
func WithInterval(d time.Duration) Option {
	return func(r *Relay) { r.interval = d }
}

// WithBatchSize configura cuántos mensajes se procesan por ciclo.
func WithBatchSize(n int) Option {
	return func(r *Relay) { r.batchSize = n }
}

// WithLogger configura el logger del relay.
func WithLogger(l *slog.Logger) Option {
	return func(r *Relay) { r.logger = l }
}

// NewRelay crea un Relay con los parámetros dados.
func NewRelay(store Store, publisher eventbus.Publisher, opts ...Option) *Relay {
	r := &Relay{
		store:     store,
		publisher: publisher,
		interval:  defaultInterval,
		batchSize: defaultBatchSize,
		logger:    slog.Default(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Start arranca el loop de publicación. Bloquea hasta que ctx sea cancelado.
// Retorna nil cuando ctx se cancela; cualquier otro error es inesperado.
func (r *Relay) Start(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		"interval", r.interval,
		"batchSize", r.batchSize,
	)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return nil
		case <-ticker.C:
			if err := r.cycle(ctx); err != nil {
				// Loguear pero no detener: el próximo tick reintentará.
				r.logger.Error("outbox relay: cycle failed", "error", err)
			}
		}
	}
}

// cycle ejecuta un ciclo de fetch-publish-mark.
func (r *Relay) cycle(ctx context.Context) error {
	msgs, err := r.store.FetchPending(ctx, r.batchSize)
	if err != nil {
		return fmt.Errorf("fetch pending: %w", err)
	}

	if len(msgs) == 0 {
		return nil
	}

	r.logger.Debug("outbox relay: publishing batch", "count", len(msgs))

	for _, msg := range msgs {
		busMsg := eventbus.Message{
			Subject: msg.Subject,
			ID:      msg.ID,
			Payload: msg.Payload,
			Headers: msg.Headers,
		}

		if err := r.publisher.Publish(ctx, busMsg); err != nil {
			// Log y continuar: no queremos que un mensaje fallido
			// bloquee la publicación de los siguientes.
			r.logger.Error("outbox relay: publish failed",
				"msgID", msg.ID,
				"subject", msg.Subject,
				"error", err,
			)
			continue
		}

		if err := r.store.MarkPublished(ctx, msg.ID); err != nil {
			// El mensaje fue publicado pero no pudimos marcarlo.
			// El próximo ciclo lo republicará; JetStream lo deduplicará por ID.
			r.logger.Error("outbox relay: mark published failed",
				"msgID", msg.ID,
				"error", err,
			)
		}
	}

	return nil
}
