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
		{TenantCreatedEvent{}, "auth.tenant.created.v1"},
		{TenantActivatedEvent{}, "auth.tenant.activated.v1"},
		{TenantSuspendedEvent{}, "auth.tenant.suspended.v1"},
		{UserRegisteredEvent{}, "auth.user.registered.v1"},
		{UserSuspendedEvent{}, "auth.user.suspended.v1"},
		{ApiKeyIssuedEvent{}, "auth.apikey.issued.v1"},
		{ApiKeyRevokedEvent{}, "auth.apikey.revoked.v1"},
		{RoleAssignedEvent{}, "auth.role.assigned.v1"},
	}
	for _, tt := range tests {
		if got := tt.event.EventType(); got != tt.want {
			t.Errorf("%T.EventType() = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestNewBasePopulatesFields(t *testing.T) {
	before := time.Now().UTC()
	b := newBase("tenant-123")
	after := time.Now().UTC()

	if b.EventID() == "" {
		t.Error("EventID should not be empty")
	}
	if b.EventTenantID() != "tenant-123" {
		t.Errorf("EventTenantID = %q, want tenant-123", b.EventTenantID())
	}
	if b.OccurredAt().Before(before) || b.OccurredAt().After(after) {
		t.Errorf("OccurredAt %v out of range [%v, %v]", b.OccurredAt(), before, after)
	}
}

func TestEventIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newBase("t").EventID()
		if seen[id] {
			t.Fatalf("duplicate EventID generated: %q", id)
		}
		seen[id] = true
	}
}

func TestNewRoleAssignedEvent(t *testing.T) {
	e := NewRoleAssignedEvent("tenant-1", "user-1", "admin", "system")
	if e.EventType() != "auth.role.assigned.v1" {
		t.Errorf("unexpected event type: %q", e.EventType())
	}
	if e.TenantID != "tenant-1" || e.UserID != "user-1" || e.Role != "admin" || e.AssignedBy != "system" {
		t.Errorf("payload mismatch: %+v", e)
	}
	if e.EventTenantID() != "tenant-1" {
		t.Errorf("EventTenantID = %q, want tenant-1", e.EventTenantID())
	}
	if e.EventID() == "" {
		t.Error("EventID should not be empty")
	}
}
