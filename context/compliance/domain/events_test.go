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
		t.Errorf("OccurredAt %v not within [%v,%v]", e.OccurredAt(), before, after)
	}
	if e.OccurredAt().Location() != time.UTC {
		t.Errorf("OccurredAt not UTC: %v", e.OccurredAt().Location())
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
	var raised Event = AlertRaisedEvent{}
	if raised.EventType() != "compliance.alert.raised.v1" {
		t.Errorf("AlertRaisedEvent type = %q", raised.EventType())
	}

	var resolved Event = AlertResolvedEvent{}
	if resolved.EventType() != "compliance.alert.resolved.v1" {
		t.Errorf("AlertResolvedEvent type = %q", resolved.EventType())
	}
}
