package domain

import (
	"testing"
	"time"
)

func TestNewNotification(t *testing.T) {
	before := time.Now().UTC()
	id := NewNotificationID()
	tid := TenantID("tenant-1")
	n := NewNotification(id, tid, "payment.captured", ChannelEmail, "user@example.com", "Pago recibido")
	after := time.Now().UTC()

	if n.ID() != id || n.TenantID() != tid {
		t.Errorf("id/tenant mismatch: %+v", n)
	}
	if n.EventType() != "payment.captured" || n.Channel() != ChannelEmail {
		t.Errorf("event/channel mismatch: %+v", n)
	}
	if n.RecipientEmail() != "user@example.com" || n.Subject() != "Pago recibido" {
		t.Errorf("recipient/subject mismatch: %+v", n)
	}
	if n.Status() != StatusPending {
		t.Errorf("status = %q, want pending", n.Status())
	}
	if n.SentAt() != nil {
		t.Errorf("sentAt should be nil on creation, got %v", n.SentAt())
	}
	if n.ErrorMsg() != "" {
		t.Errorf("errorMsg should be empty, got %q", n.ErrorMsg())
	}
	if n.CreatedAt().Before(before) || n.CreatedAt().After(after) {
		t.Errorf("createdAt %v not within [%v,%v]", n.CreatedAt(), before, after)
	}
	if n.CreatedAt().Location() != time.UTC {
		t.Error("createdAt not UTC")
	}
}

func TestReconstituteNotification(t *testing.T) {
	id := NewNotificationID()
	tid := TenantID("tenant-2")
	sentAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	n := ReconstituteNotification(
		id, tid, "payout.failed", ChannelEmail,
		"admin@example.com", "Desembolso fallido",
		StatusFailed, "bank rejected", &sentAt, createdAt,
	)

	if n.ID() != id || n.TenantID() != tid {
		t.Errorf("id/tenant not restored: %+v", n)
	}
	if n.Status() != StatusFailed || n.ErrorMsg() != "bank rejected" {
		t.Errorf("status/errorMsg not restored: %+v", n)
	}
	if !n.SentAt().Equal(sentAt) {
		t.Errorf("sentAt not restored: %v", n.SentAt())
	}
	if !n.CreatedAt().Equal(createdAt) {
		t.Errorf("createdAt not restored: %v", n.CreatedAt())
	}
}

func TestNotification_MarkSent(t *testing.T) {
	before := time.Now().UTC()
	n := NewNotification(NewNotificationID(), TenantID("t"), "onboarding.application.approved", ChannelEmail, "u@x.com", "Aprobado")
	n.MarkSent()
	after := time.Now().UTC()

	if n.Status() != StatusSent {
		t.Errorf("status = %q, want sent", n.Status())
	}
	if n.SentAt() == nil {
		t.Fatal("sentAt should be set after MarkSent")
	}
	if n.SentAt().Before(before) || n.SentAt().After(after) {
		t.Errorf("sentAt %v not within [%v,%v]", n.SentAt(), before, after)
	}
	if n.SentAt().Location() != time.UTC {
		t.Error("sentAt not UTC")
	}
}

func TestNotification_MarkFailed(t *testing.T) {
	n := NewNotification(NewNotificationID(), TenantID("t"), "onboarding.application.rejected", ChannelEmail, "u@x.com", "Rechazado")
	n.MarkFailed("smtp timeout")

	if n.Status() != StatusFailed {
		t.Errorf("status = %q, want failed", n.Status())
	}
	if n.ErrorMsg() != "smtp timeout" {
		t.Errorf("errorMsg = %q", n.ErrorMsg())
	}
	if n.SentAt() != nil {
		t.Errorf("sentAt should remain nil on failure, got %v", n.SentAt())
	}
}
