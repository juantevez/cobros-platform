package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/pkg/eventbus"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type testEvent struct{}

func (testEvent) EventID() string       { return "ev-1" }
func (testEvent) EventType() string     { return "test.thing.happened.v1" }
func (testEvent) EventTenantID() string { return "tenant-1" }
func (testEvent) OccurredAt() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }

type captureStore struct{ saved []Message }

func (s *captureStore) Save(ctx context.Context, m Message) error {
	s.saved = append(s.saved, m)
	return nil
}
func (s *captureStore) FetchPending(ctx context.Context, limit int) ([]Message, error) { return nil, nil }
func (s *captureStore) MarkPublished(ctx context.Context, id string) error             { return nil }

func TestEventPublisher_headersFromContext(t *testing.T) {
	t.Run("propagates actor and correlation id", func(t *testing.T) {
		store := &captureStore{}
		ctx := postgres.WithCorrelationID(postgres.WithActor(context.Background(), "user-42"), "corr-9")
		if err := NewEventPublisher[testEvent](store).Publish(ctx, testEvent{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		h := store.saved[0].Headers
		if h[eventbus.HeaderActor] != "user-42" || h[eventbus.HeaderCorrelationID] != "corr-9" {
			t.Errorf("headers = %+v", h)
		}
		if h["event-type"] != "test.thing.happened.v1" || h["content-type"] != "application/json" {
			t.Errorf("base headers missing: %+v", h)
		}
	})

	t.Run("worker context: actor system, no correlation id", func(t *testing.T) {
		store := &captureStore{}
		_ = NewEventPublisher[testEvent](store).Publish(context.Background(), testEvent{})
		h := store.saved[0].Headers
		if h[eventbus.HeaderActor] != "system" {
			t.Errorf("actor = %q, want system", h[eventbus.HeaderActor])
		}
		if _, ok := h[eventbus.HeaderCorrelationID]; ok {
			t.Errorf("empty correlation id should be omitted: %+v", h)
		}
	})
}
