package eventbus

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NatsPublisher implementa Publisher sobre NATS JetStream.
// El ack de JetStream garantiza que el broker recibió y persistió el mensaje.
type NatsPublisher struct {
	js jetstream.JetStream
}

// NewPublisher crea un NatsPublisher usando el Client dado.
func NewPublisher(client *Client) *NatsPublisher {
	return &NatsPublisher{js: client.JS}
}

// Publish publica un mensaje en JetStream y espera el ack del broker.
// Si msg.ID no está vacío, lo usa como Nats-Msg-Id para deduplicación.
// Los msg.Headers viajan como headers NATS.
//
// Nota: Publish es síncrono. Para publicaciones de alta frecuencia fuera
// del outbox, considerar PublishAsync (no implementado aquí en Fase 1).
func (p *NatsPublisher) Publish(ctx context.Context, msg Message) error {
	opts := []jetstream.PublishOpt{}
	if msg.ID != "" {
		opts = append(opts, jetstream.WithMsgID(msg.ID))
	}

	if _, err := p.js.PublishMsg(ctx, toNatsMsg(msg), opts...); err != nil {
		return fmt.Errorf("eventbus: publish to %q: %w", msg.Subject, err)
	}
	return nil
}

// toNatsMsg arma el mensaje NATS con el payload y los headers de la aplicación.
func toNatsMsg(msg Message) *nats.Msg {
	m := nats.NewMsg(msg.Subject)
	m.Data = msg.Payload
	for k, v := range msg.Headers {
		m.Header.Set(k, v)
	}
	return m
}
