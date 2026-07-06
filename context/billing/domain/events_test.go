package domain

import (
	"testing"
	"time"
)

func TestEventTypes(t *testing.T) {
	if (PlanCreatedEvent{}).EventType() != "billing.plan.created.v1" {
		t.Error("wrong PlanCreated event type")
	}
	if (PlanAssignedEvent{}).EventType() != "billing.plan.assigned.v1" {
		t.Error("wrong PlanAssigned event type")
	}
}

func TestNewBase(t *testing.T) {
	before := time.Now().UTC()
	b := newBase("tenant-1")
	after := time.Now().UTC()

	if b.EventID() == "" {
		t.Error("EventID should not be empty")
	}
	if b.EventTenantID() != "tenant-1" {
		t.Errorf("EventTenantID = %q, want tenant-1", b.EventTenantID())
	}
	if b.OccurredAt().Before(before) || b.OccurredAt().After(after) {
		t.Errorf("OccurredAt %v out of range", b.OccurredAt())
	}
}

func TestEventIDsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newBase("t").EventID()
		if seen[id] {
			t.Fatalf("duplicate EventID: %q", id)
		}
		seen[id] = true
	}
}
