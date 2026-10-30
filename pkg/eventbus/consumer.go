package eventbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	fetchBatchSize = 10
	fetchWait      = 2 * time.Second
	defaultAckWait = 30 * time.Second
	defaultMaxDel  = 5
)

// NatsConsumer implementa Consumer sobre NATS JetStream con pull consumers.
//
// Diseño: pull consumer durable con Ack explícito.
//   - El consumer persiste en el broker: reinicia la app sin perder posición.
//   - Pull: la app controla el ritmo de consumo (no hay push sin control).
//   - Ack explícito: el mensaje se re-entrega si el handler falla.
type NatsConsumer struct {
	js     jetstream.JetStream
	logger *slog.Logger
}

// NewConsumer crea un NatsConsumer con el Client dado.
func NewConsumer(client *Client, logger *slog.Logger) *NatsConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &NatsConsumer{js: client.JS, logger: logger}
}

// Start arranca el loop de consumo. Bloquea hasta que ctx sea cancelado.
// Crea o actualiza el consumer durable en el broker si no existe.
func (c *NatsConsumer) Start(ctx context.Context, cfg ConsumerConfig, handler Handler) error {
	maxDeliver := cfg.MaxDeliver
	if maxDeliver <= 0 {
		maxDeliver = defaultMaxDel
	}

	stream, err := c.js.Stream(ctx, cfg.Stream)
	if err != nil {
		return fmt.Errorf("eventbus: stream %q not found: %w", cfg.Stream, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       cfg.Name,
		FilterSubject: cfg.FilterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       defaultAckWait,
		MaxDeliver:    maxDeliver,
		// BackOff: re-entrega con backoff progresivo antes de ir a MaxDeliver.
		BackOff: []time.Duration{
			1 * time.Second,
			5 * time.Second,
			30 * time.Second,
		},
	})
	if err != nil {
		return fmt.Errorf("eventbus: create consumer %q: %w", cfg.Name, err)
	}

	c.logger.Info("eventbus: consumer started",
		"stream", cfg.Stream, "consumer", cfg.Name, "filter", cfg.FilterSubject)

	return c.loop(ctx, cons, handler, maxDeliver)
}

// loop es el ciclo de fetch-process-ack.
func (c *NatsConsumer) loop(ctx context.Context, cons jetstream.Consumer, handler Handler, maxDeliver int) error {
	for {
		// Salir limpiamente si el contexto fue cancelado.
		if ctx.Err() != nil {
			return nil
		}

		batch, err := cons.Fetch(fetchBatchSize, jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			// ErrNoMessages y timeout son normales; cualquier otro error es relevante.
			if !errors.Is(err, jetstream.ErrNoMessages) {
				c.logger.Warn("eventbus: fetch error", "error", err)
			}
			continue
		}

		for msg := range batch.Messages() {
			c.process(ctx, msg, handler, maxDeliver)
		}

		if batchErr := batch.Error(); batchErr != nil {
			if !errors.Is(batchErr, jetstream.ErrNoMessages) {
				c.logger.Warn("eventbus: batch error", "error", batchErr)
			}
		}
	}
}

// process invoca el handler y gestiona el ack/nak.
//
// Un error Permanent termina el mensaje (Term) sin reintentos. Cualquier otro
// lo re-entrega; en el último intento el broker ya no lo vuelve a entregar,
// así que se registra como descartado (stream y secuencia permiten recuperarlo
// del stream mientras se retenga).
func (c *NatsConsumer) process(ctx context.Context, msg jetstream.Msg, handler Handler, maxDeliver int) {
	busMsg := &Message{
		Subject: msg.Subject(),
		Payload: msg.Data(),
		Headers: headersFrom(msg.Headers()),
	}
	// Extraer el ID de deduplicación de los headers si está presente.
	if h := msg.Headers(); h != nil {
		busMsg.ID = h.Get("Nats-Msg-Id")
	}

	if err := handler(ctx, busMsg); err != nil {
		var delivered, streamSeq uint64
		if md, mdErr := msg.Metadata(); mdErr == nil {
			delivered, streamSeq = md.NumDelivered, md.Sequence.Stream
		}
		if IsPermanent(err) {
			c.logger.Error("eventbus: permanent handler error, message discarded",
				"subject", msg.Subject(), "msgID", busMsg.ID, "streamSeq", streamSeq, "error", err)
			if termErr := msg.Term(); termErr != nil {
				c.logger.Error("eventbus: term failed", "error", termErr)
			}
			return
		}
		if delivered >= uint64(maxDeliver) {
			c.logger.Error("eventbus: max deliveries reached, message discarded",
				"subject", msg.Subject(), "msgID", busMsg.ID, "streamSeq", streamSeq,
				"deliveries", delivered, "error", err)
		} else {
			c.logger.Error("eventbus: handler error, sending nak",
				"subject", msg.Subject(), "msgID", busMsg.ID, "streamSeq", streamSeq,
				"deliveries", delivered, "error", err)
		}
		// Nak con backoff: el broker reintentará según la config del consumer.
		if nakErr := msg.NakWithDelay(5 * time.Second); nakErr != nil {
			c.logger.Error("eventbus: nak failed", "error", nakErr)
		}
		return
	}

	if err := msg.Ack(); err != nil {
		c.logger.Error("eventbus: ack failed", "subject", msg.Subject(), "error", err)
	}
}

// headersFrom aplana los headers NATS a un mapa simple (primer valor de cada
// clave). Retorna nil si el mensaje no trae headers.
func headersFrom(h nats.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
