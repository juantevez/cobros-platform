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
		{ApplicationSubmittedEvent{}, "onboarding.application.submitted.v1"},
		{ApplicationSentForReviewEvent{}, "onboarding.application.sent_for_review.v1"},
		{ApplicationApprovedEvent{}, "onboarding.application.approved.v1"},
		{ApplicationRejectedEvent{}, "onboarding.application.rejected.v1"},
		{MoreInfoRequestedEvent{}, "onboarding.application.more_info.v1"},
	}
	for _, tt := range tests {
		if got := tt.event.EventType(); got != tt.want {
			t.Errorf("%T.EventType() = %q, want %q", tt.event, got, tt.want)
		}
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
