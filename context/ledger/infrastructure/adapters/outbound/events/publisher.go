package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/outbox"
)

type outboxPublisher struct {
	store outbox.Store
}

func NewEventPublisher(store outbox.Store) *outboxPublisher {
	return &outboxPublisher{store: store}
}

func (p *outboxPublisher) Publish(ctx context.Context, events ...domain.Event) error {
	for _, ev := range events {
		payload, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("ledger publisher: marshal %q: %w", ev.EventType(), err)
		}
		if err := p.store.Save(ctx, outbox.Message{
			ID:       ev.EventID(),
			TenantID: ev.EventTenantID(),
			Subject:  ev.EventType(),
			Payload:  payload,
			Headers:  map[string]string{"content-type": "application/json"},
		}); err != nil {
			return fmt.Errorf("ledger publisher: save %q: %w", ev.EventType(), err)
		}
	}
	return nil
}
