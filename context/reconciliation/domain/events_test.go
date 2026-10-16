package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReconciliationCompletedEvent_EventType(t *testing.T) {
	if got := (ReconciliationCompletedEvent{}).EventType(); got != "reconciliation.run.completed.v1" {
		t.Errorf("EventType() = %q", got)
	}
}

func TestNewBase(t *testing.T) {
	before := time.Now().UTC()
	a := newBase("tenant-1")
	b := newBase("tenant-1")

	if _, err := uuid.Parse(a.EventID()); err != nil {
		t.Errorf("event id is not a uuid: %v", err)
	}
	if a.EventID() == b.EventID() {
		t.Error("event ids should be unique")
	}
	if a.EventTenantID() != "tenant-1" {
		t.Errorf("tenant = %q", a.EventTenantID())
	}
	if a.OccurredAt().Before(before) || a.OccurredAt().Location() != time.UTC {
		t.Errorf("occurredAt = %v, want UTC now", a.OccurredAt())
	}
}
