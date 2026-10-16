package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEventTypes(t *testing.T) {
	cases := []struct {
		ev   Event
		want string
	}{
		{EndpointRegisteredEvent{}, "webhook.endpoint.registered.v1"},
		{EndpointDeactivatedEvent{}, "webhook.endpoint.deactivated.v1"},
		{DeliveryExhaustedEvent{}, "webhook.delivery.exhausted.v1"},
	}
	for _, tc := range cases {
		if got := tc.ev.EventType(); got != tc.want {
			t.Errorf("%T.EventType() = %q, want %q", tc.ev, got, tc.want)
		}
	}
}

// EventType_ existe para no chocar con el método EventType(); en el JSON del
// outbox debe viajar como "event_type".
func TestDeliveryExhaustedEvent_JSON(t *testing.T) {
	b, err := json.Marshal(DeliveryExhaustedEvent{DeliveryID: "d", EndpointID: "e", TenantID: "t", EventType_: "payment.captured"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"delivery_id":"d","endpoint_id":"e","tenant_id":"t","event_type":"payment.captured"}`
	if string(b) != want {
		t.Errorf("json = %s\nwant %s", b, want)
	}
}

func TestNewBase(t *testing.T) {
	before := time.Now().UTC()
	a, b := newBase("tenant-1"), newBase("tenant-1")

	if _, err := uuid.Parse(a.EventID()); err != nil || a.EventID() == b.EventID() {
		t.Errorf("event ids should be unique uuids: %q %q", a.EventID(), b.EventID())
	}
	if a.EventTenantID() != "tenant-1" {
		t.Errorf("tenant = %q", a.EventTenantID())
	}
	if a.OccurredAt().Before(before) || a.OccurredAt().Location() != time.UTC {
		t.Errorf("occurredAt = %v", a.OccurredAt())
	}
}
