package domain

import (
	"encoding/json"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func newTestDelivery() *WebhookDelivery {
	return NewWebhookDelivery(NewDeliveryID(), NewEndpointID(), testTenant, "payment.captured.v1", "evt-1", json.RawMessage(`{"a":1}`))
}

func deliveryWithStatus(st DeliveryStatus, attempts int, next *time.Time) *WebhookDelivery {
	return ReconstituteDelivery(NewDeliveryID(), NewEndpointID(), testTenant, "payment.captured", "evt-1",
		json.RawMessage(`{}`), st, attempts, next, nil, testNow, testNow)
}

func attemptWith(status int) DeliveryAttempt { return NewDeliveryAttempt(1, status, "", "", 10) }

func TestNewWebhookDelivery(t *testing.T) {
	id, endpointID := NewDeliveryID(), NewEndpointID()
	before := time.Now().UTC()

	d := NewWebhookDelivery(id, endpointID, testTenant, "payment.captured.v1", "evt-1", json.RawMessage(`{"a":1}`))

	if d.ID() != id || d.EndpointID() != endpointID || d.TenantID() != testTenant || d.EventID() != "evt-1" {
		t.Error("identity not set")
	}
	if d.EventType() != "payment.captured" {
		t.Errorf("event type = %q, want version stripped", d.EventType())
	}
	if string(d.Payload()) != `{"a":1}` {
		t.Errorf("payload = %s", d.Payload())
	}
	if d.Status() != StatusPending || d.AttemptCount() != 0 || len(d.Attempts()) != 0 {
		t.Errorf("status=%q attempts=%d", d.Status(), d.AttemptCount())
	}
	if d.NextRetryAt() == nil || !d.NextRetryAt().Equal(d.CreatedAt()) {
		t.Errorf("nextRetryAt = %v, want immediately (createdAt)", d.NextRetryAt())
	}
	if d.CreatedAt().Before(before) || !d.CreatedAt().Equal(d.UpdatedAt()) || d.CreatedAt().Location() != time.UTC {
		t.Errorf("createdAt=%v updatedAt=%v", d.CreatedAt(), d.UpdatedAt())
	}
	if !d.IsDueForRetry(d.CreatedAt()) {
		t.Error("new delivery should be due immediately")
	}
}

func TestWebhookDelivery_RecordAttempt(t *testing.T) {
	t.Run("success marks delivered", func(t *testing.T) {
		d := newTestDelivery()
		a := attemptWith(204)

		if err := d.RecordAttempt(a, testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusDelivered || d.AttemptCount() != 1 || d.NextRetryAt() != nil {
			t.Errorf("status=%q attempts=%d next=%v", d.Status(), d.AttemptCount(), d.NextRetryAt())
		}
		if len(d.Attempts()) != 1 || d.Attempts()[0].ID() != a.ID() || !d.UpdatedAt().Equal(testNow) {
			t.Error("attempt not recorded")
		}
		if len(d.PullEvents()) != 0 {
			t.Error("success should not emit events")
		}
	})

	t.Run("failure schedules retry per schedule", func(t *testing.T) {
		d := newTestDelivery()
		for i, wait := range RetrySchedule {
			now := testNow.Add(time.Duration(i) * time.Hour)
			if err := d.RecordAttempt(attemptWith(500), now); err != nil {
				t.Fatalf("attempt %d: unexpected error: %v", i+1, err)
			}
			if d.Status() != StatusFailed || d.AttemptCount() != i+1 {
				t.Fatalf("attempt %d: status=%q count=%d", i+1, d.Status(), d.AttemptCount())
			}
			if d.NextRetryAt() == nil || !d.NextRetryAt().Equal(now.Add(wait)) {
				t.Fatalf("attempt %d: next=%v, want now+%v", i+1, d.NextRetryAt(), wait)
			}
		}
		if len(d.PullEvents()) != 0 {
			t.Error("no event while retries remain")
		}
	})

	t.Run("MaxAttempts failures exhaust and emit event", func(t *testing.T) {
		d := newTestDelivery()
		for i := 0; i < MaxAttempts; i++ {
			if err := d.RecordAttempt(attemptWith(0), testNow); err != nil {
				t.Fatalf("attempt %d: unexpected error: %v", i+1, err)
			}
		}
		if d.Status() != StatusExhausted || d.AttemptCount() != MaxAttempts || d.NextRetryAt() != nil {
			t.Errorf("status=%q count=%d next=%v", d.Status(), d.AttemptCount(), d.NextRetryAt())
		}
		if len(d.Attempts()) != MaxAttempts {
			t.Errorf("recorded attempts = %d", len(d.Attempts()))
		}

		evs := d.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(DeliveryExhaustedEvent)
		if !ok {
			t.Fatalf("expected DeliveryExhaustedEvent, got %T", evs[0])
		}
		if ev.DeliveryID != d.ID().String() || ev.EndpointID != d.EndpointID().String() ||
			ev.TenantID != testTenant.String() || ev.EventType_ != "payment.captured" {
			t.Errorf("event = %+v", ev)
		}
	})

	t.Run("success after failures marks delivered", func(t *testing.T) {
		d := deliveryWithStatus(StatusFailed, 3, &testNow)
		if err := d.RecordAttempt(attemptWith(200), testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusDelivered || d.AttemptCount() != 4 || d.NextRetryAt() != nil {
			t.Errorf("status=%q count=%d next=%v", d.Status(), d.AttemptCount(), d.NextRetryAt())
		}
	})

	for _, st := range []DeliveryStatus{StatusDelivered, StatusExhausted} {
		t.Run("rejected from final "+st.String(), func(t *testing.T) {
			d := deliveryWithStatus(st, 2, nil)
			if err := d.RecordAttempt(attemptWith(200), testNow); err == nil {
				t.Fatal("expected error for final delivery")
			}
			if d.Status() != st || d.AttemptCount() != 2 || len(d.Attempts()) != 0 || !d.UpdatedAt().Equal(testNow) {
				t.Error("final delivery must not change")
			}
		})
	}
}

func TestWebhookDelivery_MarkReadyForRetry(t *testing.T) {
	t.Run("from failed", func(t *testing.T) {
		d := deliveryWithStatus(StatusFailed, 1, &testNow)
		before := time.Now().UTC()
		if err := d.MarkReadyForRetry(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// MarkReadyForRetry usa el reloj real (no recibe now).
		if d.Status() != StatusPending || d.UpdatedAt().Before(before) {
			t.Errorf("status=%q updatedAt=%v", d.Status(), d.UpdatedAt())
		}
	})

	for _, st := range []DeliveryStatus{StatusPending, StatusDelivered, StatusExhausted} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			d := deliveryWithStatus(st, 1, nil)
			if err := d.MarkReadyForRetry(); err == nil {
				t.Fatal("expected error")
			}
			if d.Status() != st {
				t.Errorf("status changed to %q", d.Status())
			}
		})
	}
}

func TestWebhookDelivery_IsDueForRetry(t *testing.T) {
	past, future := testNow.Add(-time.Second), testNow.Add(time.Second)
	cases := []struct {
		name   string
		status DeliveryStatus
		next   *time.Time
		want   bool
	}{
		{"pending due in the past", StatusPending, &past, true},
		{"failed due exactly now", StatusFailed, &testNow, true},
		{"failed due in the future", StatusFailed, &future, false},
		{"pending without next retry", StatusPending, nil, false},
		{"delivered", StatusDelivered, &past, false},
		{"exhausted", StatusExhausted, &past, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deliveryWithStatus(tc.status, 1, tc.next).IsDueForRetry(testNow); got != tc.want {
				t.Errorf("IsDueForRetry = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReconstituteDelivery(t *testing.T) {
	id, endpointID := NewDeliveryID(), NewEndpointID()
	next := testNow.Add(time.Minute)
	attempts := []DeliveryAttempt{ReconstituteAttempt(NewAttemptID(), 1, 500, "", "", 1, testNow)}

	d := ReconstituteDelivery(id, endpointID, testTenant, "payment.captured.v1", "evt-9", json.RawMessage(`{}`),
		StatusFailed, 1, &next, attempts, testNow.Add(-time.Hour), testNow)

	if d.ID() != id || d.EndpointID() != endpointID || d.TenantID() != testTenant || d.EventID() != "evt-9" {
		t.Error("identity not restored")
	}
	// Reconstitute no normaliza: se confía en lo persistido.
	if d.EventType() != "payment.captured.v1" {
		t.Errorf("event type = %q", d.EventType())
	}
	if d.Status() != StatusFailed || d.AttemptCount() != 1 || d.NextRetryAt() != &next || len(d.Attempts()) != 1 {
		t.Error("state not restored")
	}
	if !d.CreatedAt().Equal(testNow.Add(-time.Hour)) || !d.UpdatedAt().Equal(testNow) {
		t.Error("timestamps not restored")
	}
	if len(d.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}
