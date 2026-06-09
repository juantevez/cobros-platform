// Package outbox implementa el patrón Transactional Outbox.
//
// Problema que resuelve: publicar un evento de dominio en NATS JetStream
// y persistir el cambio en PostgreSQL no puede hacerse atómicamente entre
// dos sistemas distintos. Si publicamos primero y la tx falla, el evento
// queda huérfano. Si commiteamos primero y la publicación falla, el evento
// se pierde.
//
// Solución: el evento se escribe en la tabla outbox_messages dentro de la
// misma transacción que el cambio de dominio. Un proceso relay (cmd/worker)
// lee los mensajes pendientes y los publica en NATS, marcándolos como
// publicados. JetStream deduplica por Nats-Msg-Id en caso de re-publicación.
//
// Flujo:
//
//	Caso de uso:
//	  tx.Begin()
//	    repo.Save(aggregate)          → INSERT INTO tenants ...
//	    outbox.Save(event)            → INSERT INTO outbox_messages ...
//	  tx.Commit()
//
//	Relay (cmd/worker, cada ~1s):
//	    msgs = outbox.FetchPending()
//	    for msg in msgs:
//	      jetstream.Publish(msg)      → Nats-Msg-Id = msg.ID (dedup)
//	      outbox.MarkPublished(msg.ID)
package outbox

// Package outbox implementa el patrón Transactional Outbox.
//
// Problema que resuelve: publicar un evento de dominio en NATS JetStream
// y persistir el cambio en PostgreSQL no puede hacerse atómicamente entre
// dos sistemas distintos. Si publicamos primero y la tx falla, el evento
// queda huérfano. Si commiteamos primero y la publicación falla, el evento
// se pierde.
//
// Solución: el evento se escribe en la tabla outbox_messages dentro de la
// misma transacción que el cambio de dominio. Un proceso relay (cmd/worker)
// lee los mensajes pendientes y los publica en NATS, marcándolos como
// publicados. JetStream deduplica por Nats-Msg-Id en caso de re-publicación.
//
// Flujo:
//
//	Caso de uso:
//	  tx.Begin()
//	    repo.Save(aggregate)          → INSERT INTO tenants ...
//	    outbox.Save(event)            → INSERT INTO outbox_messages ...
//	  tx.Commit()
//
//	Relay (cmd/worker, cada ~1s):
//	    msgs = outbox.FetchPending()
//	    for msg in msgs:
//	      jetstream.Publish(msg)      → Nats-Msg-Id = msg.ID (dedup)
//	      outbox.MarkPublished(msg.ID)

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DomainEvent es la interfaz mínima que todo evento de dominio debe implementar
// para poder ser publicado a través del Outbox.
//
// Todas las interfaces domain.Event de cada contexto (auth, ledger, onboarding...)
// satisfacen esta interfaz implícitamente: tienen los mismos métodos.
// Esto permite que EventPublisher viva en pkg/ sin depender de ningún dominio concreto.
type DomainEvent interface {
	EventID() string
	EventType() string
	EventTenantID() string
	OccurredAt() time.Time
}

// EventPublisher implementa el puerto EventPublisher de cualquier contexto.
//
// Es genérico sobre E (el tipo de evento del dominio) para que la firma
// Publish(ctx, ...E) coincida exactamente con el puerto de cada contexto
// (auth/application.EventPublisher, ledger/application.EventPublisher, etc.).
// Go no permite covarianza en variadics, por lo que ...outbox.DomainEvent no
// satisface ...domain.Event aunque domain.Event implemente DomainEvent.
//
// Uso en main.go:
//
//	authPub   := outbox.NewEventPublisher[authdomain.Event](store)
//	ledgerPub := outbox.NewEventPublisher[ledgerdomain.Event](store)
type EventPublisher[E DomainEvent] struct {
	store Store
}

// NewEventPublisher crea un EventPublisher tipado para el dominio E.
func NewEventPublisher[E DomainEvent](store Store) *EventPublisher[E] {
	return &EventPublisher[E]{store: store}
}

// Publish serializa cada evento y lo guarda en outbox_messages dentro de la
// transacción activa del contexto. El Subject es el EventType del evento,
// que coincide con el subject de NATS donde el relay lo publicará.
func (p *EventPublisher[E]) Publish(ctx context.Context, events ...E) error {
	for _, ev := range events {
		payload, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("outbox publisher: marshal %q: %w", ev.EventType(), err)
		}
		if err := p.store.Save(ctx, Message{
			ID:       ev.EventID(),
			TenantID: ev.EventTenantID(),
			Subject:  ev.EventType(),
			Payload:  payload,
			Headers: map[string]string{
				"content-type": "application/json",
				"event-type":   ev.EventType(),
			},
			CreatedAt: ev.OccurredAt(),
		}); err != nil {
			return fmt.Errorf("outbox publisher: save %q: %w", ev.EventType(), err)
		}
	}
	return nil
}

// Message es un evento de dominio pendiente de publicación.
type Message struct {
	// ID es el UUID del evento; se usa como Nats-Msg-Id para deduplicación.
	ID string
	// TenantID del comercio que originó el evento. Vacío para eventos de plataforma.
	TenantID string
	// Subject es el destino en NATS, ej: "auth.tenant.created.v1".
	Subject string
	// Payload es el cuerpo del evento serializado en JSON.
	Payload []byte
	// Headers son metadatos adicionales opcionales.
	Headers     map[string]string
	CreatedAt   time.Time
	PublishedAt *time.Time // nil = pendiente
}

// Store persiste y recupera mensajes del outbox.
//
// IMPORTANTE: la implementación de Save debe ejecutarse dentro de la
// transacción activa del contexto (usando postgres.ConnFromContext).
// FetchPending y MarkPublished corren fuera de transacciones de negocio.
type Store interface {
	// Save persiste un mensaje en la misma transacción del contexto.
	// El ID debe ser único (UUID del evento).
	Save(ctx context.Context, msg Message) error

	// FetchPending retorna hasta n mensajes no publicados, en orden FIFO.
	// Usa SKIP LOCKED para permitir múltiples instancias del relay.
	FetchPending(ctx context.Context, n int) ([]Message, error)

	// MarkPublished registra la publicación exitosa del mensaje.
	MarkPublished(ctx context.Context, id string) error
}
