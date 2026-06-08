package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/outbox"
)

// outboxPublisher implementa application.EventPublisher escribiendo en el Outbox.
//
// Traduce domain.Event → outbox.Message y lo persiste en la misma transacción
// que el cambio de dominio (ConnFromContext en outbox.Store.Save).
// El relay de cmd/worker leerá los mensajes y los publicará en NATS JetStream.
type outboxPublisher struct {
	store outbox.Store
}

// NewEventPublisher crea un EventPublisher respaldado por el Outbox de PostgreSQL.
func NewEventPublisher(store outbox.Store) *outboxPublisher {
	return &outboxPublisher{store: store}
}

// Publish serializa cada evento y lo guarda en la tabla outbox_messages
// dentro de la transacción activa del contexto.
//
// El Subject del mensaje es el EventType del evento (ej: "auth.tenant.created.v1"),
// que coincide con el subject de NATS donde el relay lo publicará.
func (p *outboxPublisher) Publish(ctx context.Context, events ...domain.Event) error {
	for _, ev := range events {
		payload, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("event publisher: marshal %q: %w", ev.EventType(), err)
		}

		msg := outbox.Message{
			ID:       ev.EventID(),
			TenantID: ev.EventTenantID(),
			Subject:  ev.EventType(), // "auth.tenant.created.v1" → subject en NATS
			Payload:  payload,
			Headers: map[string]string{
				"content-type": "application/json",
				"event-type":   ev.EventType(),
			},
			CreatedAt: ev.OccurredAt(),
		}

		if err := p.store.Save(ctx, msg); err != nil {
			return fmt.Errorf("event publisher: save %q: %w", ev.EventType(), err)
		}
	}
	return nil
}
