package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

func validEvent() IncomingEvent {
	return IncomingEvent{
		TenantID:   tenantA,
		EventType:  "payment.captured.v1",
		EventID:    "evt-123",
		OccurredAt: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
		Data:       json.RawMessage(`{"payment_id":"pay-1","amount":10000}`),
	}
}

func TestDispatchEventUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("creates one pending delivery per subscribed endpoint", func(t *testing.T) {
		e1, e2 := newEndpoint(tenantA), newEndpoint(tenantA)
		endpoints := newEndpointRepo()
		endpoints.active = []*domain.WebhookEndpoint{e1, e2}
		deliveries := newDeliveryRepo()

		if err := NewDispatchEventUseCase(endpoints, deliveries).Execute(ctx, validEvent()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoints.lastTenant != domain.TenantID(tenantA) || endpoints.lastEventType != "payment.captured.v1" {
			t.Errorf("lookup with tenant=%q event=%q", endpoints.lastTenant, endpoints.lastEventType)
		}
		if len(deliveries.saved) != 2 {
			t.Fatalf("expected 2 deliveries, got %d", len(deliveries.saved))
		}
		for i, e := range []*domain.WebhookEndpoint{e1, e2} {
			d := deliveries.saved[i]
			if d.EndpointID() != e.ID() || d.TenantID() != domain.TenantID(tenantA) {
				t.Errorf("delivery %d not linked to endpoint/tenant", i)
			}
			if d.Status() != domain.StatusPending || d.EventID() != "evt-123" || d.EventType() != "payment.captured" {
				t.Errorf("delivery %d = status %q event %q type %q", i, d.Status(), d.EventID(), d.EventType())
			}
		}
	})

	t.Run("payload is the webhook envelope", func(t *testing.T) {
		endpoints := newEndpointRepo()
		endpoints.active = []*domain.WebhookEndpoint{newEndpoint(tenantA)}
		deliveries := newDeliveryRepo()
		ev := validEvent()

		if err := NewDispatchEventUseCase(endpoints, deliveries).Execute(ctx, ev); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := deliveries.saved[0]
		var env WebhookEnvelope
		if err := json.Unmarshal(d.Payload(), &env); err != nil {
			t.Fatalf("payload is not an envelope: %v", err)
		}
		if env.EventType != "payment.captured.v1" || env.EventID != "evt-123" || env.DeliveryID != d.ID().String() {
			t.Errorf("envelope = %+v", env)
		}
		if !env.OccurredAt.Equal(ev.OccurredAt) || string(env.Data) != string(ev.Data) {
			t.Errorf("envelope time/data = %v %s", env.OccurredAt, env.Data)
		}
	})

	t.Run("no subscribed endpoints is a no-op", func(t *testing.T) {
		deliveries := newDeliveryRepo()
		if err := NewDispatchEventUseCase(newEndpointRepo(), deliveries).Execute(ctx, validEvent()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(deliveries.saved) != 0 {
			t.Error("nothing should be saved")
		}
	})

	t.Run("existing delivery for endpoint and event is skipped", func(t *testing.T) {
		e1, e2 := newEndpoint(tenantA), newEndpoint(tenantA)
		endpoints := newEndpointRepo()
		endpoints.active = []*domain.WebhookEndpoint{e1, e2}
		deliveries := newDeliveryRepo()
		deliveries.existing[idempotencyKey{e1.ID(), "evt-123"}] = deliveryWith(e1, domain.StatusDelivered, 1)

		if err := NewDispatchEventUseCase(endpoints, deliveries).Execute(ctx, validEvent()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(deliveries.saved) != 1 || deliveries.saved[0].EndpointID() != e2.ID() {
			t.Errorf("only e2 should get a new delivery, saved = %d", len(deliveries.saved))
		}
	})

	t.Run("failure on one endpoint does not block the others", func(t *testing.T) {
		e1, e2, e3 := newEndpoint(tenantA), newEndpoint(tenantA), newEndpoint(tenantA)
		endpoints := newEndpointRepo()
		endpoints.active = []*domain.WebhookEndpoint{e1, e2, e3}
		deliveries := newDeliveryRepo()
		deliveries.idemErrFor = map[domain.EndpointID]error{e1.ID(): errBoom}
		deliveries.saveErrFor = map[domain.EndpointID]error{e2.ID(): errBoom}

		err := NewDispatchEventUseCase(endpoints, deliveries).Execute(ctx, validEvent())

		if len(deliveries.saved) != 1 || deliveries.saved[0].EndpointID() != e3.ID() {
			t.Errorf("e3 should still get its delivery, saved = %d", len(deliveries.saved))
		}
		// El error se propaga para que el consumer haga nak y la re-entrega
		// reintente e1 y e2 (e3 se saltea por idempotencia).
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		for _, id := range []domain.EndpointID{e1.ID(), e2.ID()} {
			if !strings.Contains(err.Error(), id.String()) {
				t.Errorf("error should name failing endpoint %s: %v", id, err)
			}
		}
	})

	t.Run("event data that is not valid JSON creates no delivery", func(t *testing.T) {
		endpoints := newEndpointRepo()
		endpoints.active = []*domain.WebhookEndpoint{newEndpoint(tenantA)}
		deliveries := newDeliveryRepo()
		ev := validEvent()
		ev.Data = json.RawMessage(`{not json`)

		err := NewDispatchEventUseCase(endpoints, deliveries).Execute(ctx, ev)

		if err == nil || !strings.Contains(err.Error(), "marshal envelope") {
			t.Fatalf("expected marshal error, got %v", err)
		}
		if len(deliveries.saved) != 0 {
			t.Error("envelope cannot be built, nothing should be saved")
		}
	})

	t.Run("invalid tenant", func(t *testing.T) {
		ev := validEvent()
		ev.TenantID = "nope"
		err := NewDispatchEventUseCase(newEndpointRepo(), newDeliveryRepo()).Execute(ctx, ev)
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("endpoint lookup error is wrapped", func(t *testing.T) {
		endpoints := newEndpointRepo()
		endpoints.activeErr = errBoom
		err := NewDispatchEventUseCase(endpoints, newDeliveryRepo()).Execute(ctx, validEvent())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "find endpoints") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
