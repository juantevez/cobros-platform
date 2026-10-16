package domain

import (
	"testing"
	"time"
)

func TestNewNotificationPreference(t *testing.T) {
	before := time.Now().UTC()
	tid := TenantID("tenant-1")
	p := NewNotificationPreference(tid, "payment.captured", ChannelEmail, true, "billing@example.com")
	after := time.Now().UTC()

	if p.TenantID() != tid || p.EventType() != "payment.captured" {
		t.Errorf("tenant/event mismatch: %+v", p)
	}
	if p.Channel() != ChannelEmail || !p.Enabled() {
		t.Errorf("channel/enabled mismatch: %+v", p)
	}
	if p.RecipientEmail() != "billing@example.com" {
		t.Errorf("recipientEmail = %q", p.RecipientEmail())
	}
	if p.UpdatedAt().Before(before) || p.UpdatedAt().After(after) {
		t.Errorf("updatedAt %v not within [%v,%v]", p.UpdatedAt(), before, after)
	}
	if p.UpdatedAt().Location() != time.UTC {
		t.Error("updatedAt not UTC")
	}
}

func TestNewNotificationPreference_Disabled(t *testing.T) {
	p := NewNotificationPreference(TenantID("t"), "payment.failed", ChannelEmail, false, "")
	if p.Enabled() {
		t.Error("preference should be disabled")
	}
	if p.RecipientEmail() != "" {
		t.Errorf("recipientEmail should be empty, got %q", p.RecipientEmail())
	}
}

func TestReconstitutePreference(t *testing.T) {
	tid := TenantID("tenant-2")
	updatedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	p := ReconstitutePreference(tid, "payout.confirmed", ChannelEmail, true, "ops@example.com", updatedAt)

	if p.TenantID() != tid || p.EventType() != "payout.confirmed" {
		t.Errorf("tenant/event not restored: %+v", p)
	}
	if p.Channel() != ChannelEmail || !p.Enabled() {
		t.Errorf("channel/enabled not restored: %+v", p)
	}
	if p.RecipientEmail() != "ops@example.com" {
		t.Errorf("recipientEmail not restored: %q", p.RecipientEmail())
	}
	if !p.UpdatedAt().Equal(updatedAt) {
		t.Errorf("updatedAt not restored: %v", p.UpdatedAt())
	}
}
