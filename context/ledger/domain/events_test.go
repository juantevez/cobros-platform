package domain

import (
	"testing"
	"time"
)

func TestEventTypes(t *testing.T) {
	tests := []struct {
		event Event
		want  string
	}{
		{AccountCreatedEvent{}, "ledger.account.created.v1"},
		{EntryPostedEvent{}, "ledger.entry.posted.v1"},
		{EntryReversedEvent{}, "ledger.entry.reversed.v1"},
	}
	for _, tt := range tests {
		if got := tt.event.EventType(); got != tt.want {
			t.Errorf("%T.EventType() = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestNewBase(t *testing.T) {
	before := time.Now().UTC()
	b := newBase("tenant-xyz")
	after := time.Now().UTC()

	if b.EventID() == "" {
		t.Error("EventID should not be empty")
	}
	if b.EventTenantID() != "tenant-xyz" {
		t.Errorf("EventTenantID = %q, want tenant-xyz", b.EventTenantID())
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
