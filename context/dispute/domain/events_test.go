package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBaseEvent(t *testing.T) {
	before := time.Now().UTC()
	e := newBase("tenant-x")
	after := time.Now().UTC()

	if _, err := uuid.Parse(e.EventID()); err != nil {
		t.Errorf("EventID not a uuid: %q", e.EventID())
	}
	if e.EventTenantID() != "tenant-x" {
		t.Errorf("EventTenantID = %q", e.EventTenantID())
	}
	if e.OccurredAt().Before(before) || e.OccurredAt().After(after) {
		t.Errorf("OccurredAt out of range: %v", e.OccurredAt())
	}
	if e.OccurredAt().Location() != time.UTC {
		t.Errorf("OccurredAt not UTC")
	}
}

func TestBaseEvent_uniqueIDs(t *testing.T) {
	id1 := newBase("t").EventID()
	id2 := newBase("t").EventID()
	if id1 == id2 {
		t.Error("event IDs should be unique")
	}
}

func TestEventTypes(t *testing.T) {
	var opened Event = DisputeOpenedEvent{}
	if opened.EventType() != "dispute.opened.v1" {
		t.Errorf("DisputeOpenedEvent type = %q", opened.EventType())
	}

	var resolved Event = DisputeResolvedEvent{}
	if resolved.EventType() != "dispute.resolved.v1" {
		t.Errorf("DisputeResolvedEvent type = %q", resolved.EventType())
	}
}
