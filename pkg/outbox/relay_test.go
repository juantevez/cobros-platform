package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

var errBoom = errors.New("boom")

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	pending   []Message
	fetchErr  error
	published []string
}

func (s *fakeStore) Save(ctx context.Context, msg Message) error { return nil }
func (s *fakeStore) FetchPending(ctx context.Context, n int) ([]Message, error) {
	return s.pending, s.fetchErr
}
func (s *fakeStore) MarkPublished(ctx context.Context, id string) error {
	s.published = append(s.published, id)
	return nil
}

type fakePublisher struct {
	sent   []eventbus.Message
	failID string
}

func (p *fakePublisher) Publish(ctx context.Context, msg eventbus.Message) error {
	if msg.ID == p.failID {
		return errBoom
	}
	p.sent = append(p.sent, msg)
	return nil
}

func newTestRelay(s Store, p eventbus.Publisher) *Relay {
	return NewRelay(s, p, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
}

// ── withOccurredAt ────────────────────────────────────────────────────────────

func TestWithOccurredAt(t *testing.T) {
	at := time.Date(2026, 10, 16, 9, 30, 0, 123, time.FixedZone("ART", -3*3600))

	t.Run("adds UTC RFC3339Nano timestamp and keeps other headers", func(t *testing.T) {
		in := map[string]string{"event-type": "payment.captured.v1"}
		got := withOccurredAt(in, at)

		want := map[string]string{
			"event-type":              "payment.captured.v1",
			eventbus.HeaderOccurredAt: "2026-10-16T12:30:00.000000123Z",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if _, mutated := in[eventbus.HeaderOccurredAt]; mutated {
			t.Error("input map must not be mutated")
		}
	})

	t.Run("nil headers", func(t *testing.T) {
		got := withOccurredAt(nil, at)
		if len(got) != 1 || got[eventbus.HeaderOccurredAt] == "" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("existing value is preserved", func(t *testing.T) {
		got := withOccurredAt(map[string]string{eventbus.HeaderOccurredAt: "keep"}, at)
		if got[eventbus.HeaderOccurredAt] != "keep" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("zero time adds nothing", func(t *testing.T) {
		got := withOccurredAt(map[string]string{"a": "b"}, time.Time{})
		if !reflect.DeepEqual(got, map[string]string{"a": "b"}) {
			t.Errorf("got %v", got)
		}
	})
}

// ── cycle ─────────────────────────────────────────────────────────────────────

func TestRelay_Cycle(t *testing.T) {
	created := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	t.Run("publishes pending messages with occurred-at and marks them", func(t *testing.T) {
		store := &fakeStore{pending: []Message{
			{ID: "m1", Subject: "payment.captured.v1", Payload: []byte(`{}`), Headers: map[string]string{"event-type": "payment.captured.v1"}, CreatedAt: created},
			{ID: "m2", Subject: "ledger.entry.posted.v1", Payload: []byte(`{}`), CreatedAt: created.Add(time.Second)},
		}}
		pub := &fakePublisher{}

		if err := newTestRelay(store, pub).cycle(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pub.sent) != 2 {
			t.Fatalf("sent %d messages, want 2", len(pub.sent))
		}
		first := pub.sent[0]
		if first.ID != "m1" || first.Subject != "payment.captured.v1" || first.Headers["event-type"] != "payment.captured.v1" {
			t.Errorf("first = %+v", first)
		}
		if first.Headers[eventbus.HeaderOccurredAt] != "2026-10-16T12:00:00Z" {
			t.Errorf("occurred-at = %q", first.Headers[eventbus.HeaderOccurredAt])
		}
		if pub.sent[1].Headers[eventbus.HeaderOccurredAt] != "2026-10-16T12:00:01Z" {
			t.Errorf("occurred-at without prior headers = %q", pub.sent[1].Headers[eventbus.HeaderOccurredAt])
		}
		if !reflect.DeepEqual(store.published, []string{"m1", "m2"}) {
			t.Errorf("marked = %v", store.published)
		}
	})

	t.Run("failed publish is not marked and does not block the rest", func(t *testing.T) {
		store := &fakeStore{pending: []Message{{ID: "m1", CreatedAt: created}, {ID: "m2", CreatedAt: created}}}
		pub := &fakePublisher{failID: "m1"}

		if err := newTestRelay(store, pub).cycle(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(store.published, []string{"m2"}) {
			t.Errorf("marked = %v, want [m2]", store.published)
		}
	})

	t.Run("fetch error is returned", func(t *testing.T) {
		err := newTestRelay(&fakeStore{fetchErr: errBoom}, &fakePublisher{}).cycle(context.Background())
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
