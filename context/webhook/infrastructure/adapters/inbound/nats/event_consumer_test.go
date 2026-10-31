package nats

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/application"
	"github.com/juantevez/cobros-platform/context/webhook/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var errBoom = errors.New("boom")

const tenantA = "11111111-1111-1111-1111-111111111111"

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeEndpoints struct {
	active        []*domain.WebhookEndpoint
	err           error
	lastEventType string
	// ctxTenant: tenant del contexto con el que se buscaron endpoints (RLS).
	ctxTenant string
}

func (f *fakeEndpoints) Save(context.Context, *domain.WebhookEndpoint) error   { return nil }
func (f *fakeEndpoints) Update(context.Context, *domain.WebhookEndpoint) error { return nil }
func (f *fakeEndpoints) FindByID(context.Context, domain.EndpointID) (*domain.WebhookEndpoint, error) {
	return nil, domain.ErrEndpointNotFound
}
func (f *fakeEndpoints) FindByTenant(context.Context, domain.TenantID) ([]*domain.WebhookEndpoint, error) {
	return nil, nil
}
func (f *fakeEndpoints) FindActiveByTenantAndEvent(ctx context.Context, t domain.TenantID, eventType string) ([]*domain.WebhookEndpoint, error) {
	f.lastEventType = eventType
	f.ctxTenant, _ = pkgpostgres.TenantIDFromContext(ctx)
	return f.active, f.err
}

type fakeDeliveries struct{ saved []*domain.WebhookDelivery }

func (f *fakeDeliveries) Save(ctx context.Context, d *domain.WebhookDelivery) error {
	f.saved = append(f.saved, d)
	return nil
}
func (f *fakeDeliveries) Update(context.Context, *domain.WebhookDelivery, int) error { return nil }
func (f *fakeDeliveries) Claim(context.Context, domain.DeliveryID, int, time.Time, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeDeliveries) FindByID(context.Context, domain.DeliveryID) (*domain.WebhookDelivery, error) {
	return nil, domain.ErrDeliveryNotFound
}
func (f *fakeDeliveries) FindByEventAndEndpoint(context.Context, domain.EndpointID, string) (*domain.WebhookDelivery, error) {
	return nil, domain.ErrDeliveryNotFound
}
func (f *fakeDeliveries) ListByTenant(context.Context, domain.TenantID, int) ([]*domain.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeDeliveries) ListDueForRetry(context.Context, time.Time, int) ([]*domain.WebhookDelivery, error) {
	return nil, nil
}

type fakeBus struct {
	cfg     eventbus.ConsumerConfig
	handler eventbus.Handler
}

func (b *fakeBus) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	b.cfg, b.handler = cfg, h
	return errBoom
}

// ── Helpers ───────────────────────────────────────────────────────────────────

type env struct {
	endpoints  *fakeEndpoints
	deliveries *fakeDeliveries
	consumer   *EventConsumer
}

func newEnv(bus eventbus.Consumer) *env {
	e, _ := domain.NewWebhookEndpoint(domain.NewEndpointID(), domain.TenantID(tenantA),
		"https://merchant.example.com/hooks", "whsec_x", []string{"payment.captured"}, "")
	endpoints := &fakeEndpoints{active: []*domain.WebhookEndpoint{e}}
	deliveries := &fakeDeliveries{}
	uc := application.NewDispatchEventUseCase(endpoints, deliveries)
	return &env{
		endpoints:  endpoints,
		deliveries: deliveries,
		consumer:   NewEventConsumer(bus, uc, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
}

func message(payload string) *eventbus.Message {
	return &eventbus.Message{Subject: "payment.captured.v1", ID: "nats-msg-1", Payload: []byte(payload)}
}

// envelope decodifica el payload guardado en la única delivery creada.
func (e *env) envelope(t *testing.T) application.WebhookEnvelope {
	t.Helper()
	if len(e.deliveries.saved) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(e.deliveries.saved))
	}
	var env application.WebhookEnvelope
	if err := json.Unmarshal(e.deliveries.saved[0].Payload(), &env); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}
	return env
}

// ── Start ─────────────────────────────────────────────────────────────────────

func TestStartConsumers(t *testing.T) {
	cases := []struct {
		name                  string
		start                 func(*EventConsumer, context.Context) error
		stream, cname, filter string
	}{
		{"payment", (*EventConsumer).StartPaymentConsumer, "PAYMENT", "webhook-payment-consumer", "payment.>"},
		{"payout", (*EventConsumer).StartPayoutConsumer, "PAYOUT", "webhook-payout-consumer", "payout.>"},
		{"onboarding", (*EventConsumer).StartOnboardingConsumer, "ONBOARDING", "webhook-onboarding-consumer", "onboarding.>"},
		{"auth", (*EventConsumer).StartAuthConsumer, "AUTH", "webhook-auth-consumer", "auth.tenant.>"},
		{"dispute", (*EventConsumer).StartDisputeConsumer, "DISPUTE", "webhook-dispute-consumer", "dispute.>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{}
			err := tc.start(newEnv(bus).consumer, context.Background())

			if !errors.Is(err, errBoom) {
				t.Errorf("Start error should be propagated, got %v", err)
			}
			want := eventbus.ConsumerConfig{Stream: tc.stream, Name: tc.cname, FilterSubject: tc.filter, MaxDeliver: 3}
			if bus.cfg != want || bus.handler == nil {
				t.Errorf("config = %+v, want %+v", bus.cfg, want)
			}
		})
	}
}

// ── handle ────────────────────────────────────────────────────────────────────

func TestHandle(t *testing.T) {
	ctx := context.Background()

	t.Run("creates delivery with subject as event type and raw payload as data", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		payload := `{"tenant_id":"` + tenantA + `","event_id":"evt-1","occurred_at":"2026-10-16T09:00:00Z","amount":100}`

		if err := e.consumer.handle(ctx, message(payload)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.endpoints.lastEventType != "payment.captured.v1" {
			t.Errorf("event type = %q", e.endpoints.lastEventType)
		}
		// Sin el tenant en el contexto, con un rol sin BYPASSRLS los repos no
		// verían endpoints y el evento se descartaría en silencio.
		if e.endpoints.ctxTenant != tenantA {
			t.Errorf("tenant in context = %q, want %q", e.endpoints.ctxTenant, tenantA)
		}
		env := e.envelope(t)
		if env.EventType != "payment.captured.v1" || env.EventID != "evt-1" || string(env.Data) != payload {
			t.Errorf("envelope = %+v", env)
		}
		if !env.OccurredAt.Equal(time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)) {
			t.Errorf("occurred_at = %v", env.OccurredAt)
		}
	})

	eventIDs := []struct {
		name, payload, want string
	}{
		{"id field wins", `"id":"from-id","event_id":"from-event-id"`, "from-id"},
		{"event_id fallback", `"event_id":"from-event-id"`, "from-event-id"},
		{"NATS message id fallback", `"amount":1`, "nats-msg-1"},
	}
	for _, tc := range eventIDs {
		t.Run("event id: "+tc.name, func(t *testing.T) {
			e := newEnv(&fakeBus{})
			if err := e.consumer.handle(ctx, message(`{"tenant_id":"`+tenantA+`",`+tc.payload+`}`)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := e.deliveries.saved[0].EventID(); got != tc.want {
				t.Errorf("event id = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("occurred_at falls back to outbox header", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		m := message(`{"tenant_id":"` + tenantA + `"}`)
		m.Headers = map[string]string{eventbus.HeaderOccurredAt: "2026-10-16T08:30:00.25Z"}

		if err := e.consumer.handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := e.envelope(t).OccurredAt, time.Date(2026, 10, 16, 8, 30, 0, 250_000_000, time.UTC); !got.Equal(want) {
			t.Errorf("occurred_at = %v, want %v", got, want)
		}
	})

	t.Run("payload occurred_at wins over header", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		m := message(`{"tenant_id":"` + tenantA + `","occurred_at":"2026-10-16T09:00:00Z"}`)
		m.Headers = map[string]string{eventbus.HeaderOccurredAt: "2026-10-16T08:30:00Z"}

		if err := e.consumer.handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := e.envelope(t).OccurredAt; !got.Equal(time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)) {
			t.Errorf("occurred_at = %v", got)
		}
	})

	t.Run("invalid occurred-at header defaults to now", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		m := message(`{"tenant_id":"` + tenantA + `"}`)
		m.Headers = map[string]string{eventbus.HeaderOccurredAt: "yesterday"}
		before := time.Now().UTC()

		if err := e.consumer.handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := e.envelope(t).OccurredAt; got.Before(before) {
			t.Errorf("occurred_at = %v, want ~now", got)
		}
	})

	t.Run("missing occurred_at defaults to now", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		before := time.Now().UTC()
		if err := e.consumer.handle(ctx, message(`{"tenant_id":"`+tenantA+`"}`)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := e.envelope(t).OccurredAt; got.Before(before) {
			t.Errorf("occurred_at = %v, want ~now", got)
		}
	})

	for _, payload := range []string{`{not json`, `{"event_id":"e"}`, `{"tenant_id":""}`, `{"tenant_id":"tenant-1"}`} {
		t.Run("acks without dispatching: "+payload, func(t *testing.T) {
			e := newEnv(&fakeBus{})
			if err := e.consumer.handle(ctx, message(payload)); err != nil {
				t.Fatalf("should be acked, got %v", err)
			}
			if len(e.deliveries.saved) != 0 || e.endpoints.lastEventType != "" {
				t.Error("nothing should be dispatched")
			}
		})
	}

	t.Run("dispatch error naks for retry", func(t *testing.T) {
		e := newEnv(&fakeBus{})
		e.endpoints.err = errBoom
		if err := e.consumer.handle(ctx, message(`{"tenant_id":"`+tenantA+`"}`)); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// Cada tipo del catálogo suscribible tiene que llegar por alguno de los
// durables (el filtro real de cada Start*): si no, el comercio podría
// suscribirse a algo que nunca recibe.
func TestSubscribableEventsPassConsumerFilters(t *testing.T) {
	var filters []string
	for _, start := range []func(*EventConsumer, context.Context) error{
		(*EventConsumer).StartPaymentConsumer, (*EventConsumer).StartPayoutConsumer,
		(*EventConsumer).StartOnboardingConsumer, (*EventConsumer).StartAuthConsumer,
		(*EventConsumer).StartDisputeConsumer,
	} {
		bus := &fakeBus{}
		_ = start(newEnv(bus).consumer, context.Background())
		filters = append(filters, bus.cfg.FilterSubject)
	}

	for _, ev := range domain.SubscribableEvents {
		subject := ev + ".v1"
		matched := false
		for _, f := range filters {
			matched = matched || strings.HasPrefix(subject, strings.TrimSuffix(f, ">"))
		}
		if !matched {
			t.Errorf("%s is subscribable but no consumer filter %v forwards it", ev, filters)
		}
	}
}
