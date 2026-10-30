package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

func validTenant() string { return uuid.NewString() }

func newSendUC(nr *fakeNotifRepo, pr *fakePrefRepo, es *fakeEmailSender, cr fakeContactReader) *SendNotificationUseCase {
	return NewSendNotificationUseCase(nr, pr, es, cr, fixedClock{}, discardLogger())
}

func TestSendNotificationUseCase_Execute(t *testing.T) {
	baseCmd := func() SendNotificationCmd {
		return SendNotificationCmd{
			TenantID:  validTenant(),
			EventType: "payment.captured",
			TemplateData: map[string]string{
				"amount":   "1000",
				"currency": "ARS",
			},
		}
	}

	t.Run("sends to admin email and saves sent record", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 1 {
			t.Fatalf("expected 1 email sent, got %d", len(es.sent))
		}
		if es.sent[0].to != "admin@x.com" {
			t.Errorf("to = %q", es.sent[0].to)
		}
		if es.sent[0].subject == "" || es.sent[0].subject == "{{.amount}}" {
			t.Errorf("subject not rendered: %q", es.sent[0].subject)
		}
		if len(nr.saved) != 1 {
			t.Fatalf("expected 1 saved record, got %d", len(nr.saved))
		}
		if nr.saved[0].Status() != domain.StatusSent {
			t.Errorf("status = %q, want sent", nr.saved[0].Status())
		}
	})

	t.Run("uses preference recipient email over admin", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		pr.byEvent["payment.captured"] = domain.NewNotificationPreference(
			domain.TenantID("t"), "payment.captured", domain.ChannelEmail, true, "override@x.com", time.Now())
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 1 || es.sent[0].to != "override@x.com" {
			t.Fatalf("expected send to override email, got %+v", es.sent)
		}
	})

	t.Run("falls back to admin when preference has no explicit email", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		pr.byEvent["payment.captured"] = domain.NewNotificationPreference(
			domain.TenantID("t"), "payment.captured", domain.ChannelEmail, true, "", time.Now())
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 1 || es.sent[0].to != "admin@x.com" {
			t.Fatalf("expected fallback to admin, got %+v", es.sent)
		}
	})

	t.Run("skips silently when preference disabled", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		pr.byEvent["payment.captured"] = domain.NewNotificationPreference(
			domain.TenantID("t"), "payment.captured", domain.ChannelEmail, false, "x@x.com", time.Now())
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 0 {
			t.Errorf("expected no email sent, got %d", len(es.sent))
		}
		if len(nr.saved) != 0 {
			t.Errorf("expected no record saved, got %d", len(nr.saved))
		}
	})

	t.Run("no template is a silent skip", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		cmd := baseCmd()
		cmd.EventType = "unknown.event"
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 0 || len(nr.saved) != 0 {
			t.Error("expected nothing to happen for unknown event")
		}
	})

	t.Run("skips when admin email lookup fails", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{err: errBoom})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 0 || len(nr.saved) != 0 {
			t.Error("expected skip when no recipient resolvable")
		}
	})

	t.Run("records failure when email send fails", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		es := &fakeEmailSender{err: errBoom}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nr.saved) != 1 {
			t.Fatalf("expected 1 saved record, got %d", len(nr.saved))
		}
		if nr.saved[0].Status() != domain.StatusFailed {
			t.Errorf("status = %q, want failed", nr.saved[0].Status())
		}
		if nr.saved[0].ErrorMsg() != errBoom.Error() {
			t.Errorf("errorMsg = %q", nr.saved[0].ErrorMsg())
		}
	})

	t.Run("send success is not affected by save error", func(t *testing.T) {
		nr := &fakeNotifRepo{saveErr: errBoom}
		pr := newPrefRepo()
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("save error should not propagate, got %v", err)
		}
		if len(es.sent) != 1 {
			t.Errorf("email should still be sent")
		}
	})

	t.Run("invalid tenant id returns error", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		pr := newPrefRepo()
		es := &fakeEmailSender{}
		uc := newSendUC(nr, pr, es, fakeContactReader{email: "admin@x.com"})

		cmd := baseCmd()
		cmd.TenantID = "not-a-uuid"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})
}

func TestListNotificationsUseCase_Execute(t *testing.T) {
	seed := func() []*domain.Notification {
		n := domain.NewNotification(domain.NewNotificationID(), domain.TenantID("t"),
			"payment.captured", domain.ChannelEmail, "u@x.com", "Pago", time.Now())
		n.MarkSent(time.Now())
		return []*domain.Notification{n}
	}

	t.Run("maps records to views", func(t *testing.T) {
		nr := &fakeNotifRepo{listed: seed()}
		uc := NewListNotificationsUseCase(nr)

		views, err := uc.Execute(context.Background(), validTenant(), 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 1 {
			t.Fatalf("expected 1 view, got %d", len(views))
		}
		if views[0].Status != "sent" || views[0].SentAt == "" {
			t.Errorf("view mapping wrong: %+v", views[0])
		}
		if nr.gotLimit != 10 {
			t.Errorf("limit not forwarded: %d", nr.gotLimit)
		}
	})

	t.Run("defaults limit to 50 when non-positive", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		uc := NewListNotificationsUseCase(nr)

		if _, err := uc.Execute(context.Background(), validTenant(), 0); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nr.gotLimit != 50 {
			t.Errorf("expected default limit 50, got %d", nr.gotLimit)
		}
	})

	t.Run("limit capped at 200", func(t *testing.T) {
		nr := &fakeNotifRepo{}
		if _, err := NewListNotificationsUseCase(nr).Execute(context.Background(), validTenant(), 1_000_000); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nr.gotLimit != 200 {
			t.Errorf("limit = %d, want 200", nr.gotLimit)
		}
	})

	t.Run("invalid tenant id returns error", func(t *testing.T) {
		uc := NewListNotificationsUseCase(&fakeNotifRepo{})
		if _, err := uc.Execute(context.Background(), "bad", 10); err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})

	t.Run("propagates repo error", func(t *testing.T) {
		uc := NewListNotificationsUseCase(&fakeNotifRepo{listErr: errBoom})
		if _, err := uc.Execute(context.Background(), validTenant(), 10); err == nil {
			t.Fatal("expected error from repo")
		}
	})
}

func TestSendNotification_KYCEventsAreDelivered(t *testing.T) {
	// Regresión: los templates de KYC se registraban como kyc.approved /
	// kyc.rejected, pero el evento llega como onboarding.application.*.v1 y
	// se normaliza a onboarding.application.*: el lookup fallaba y el email
	// se descartaba en silencio.
	for _, tc := range []struct {
		subject, wantInBody string
		data                map[string]string
	}{
		{"onboarding.application.approved.v1", "aprobada", map[string]string{}},
		{"onboarding.application.rejected.v1", "docs_invalid", map[string]string{"rejection_reason": "docs_invalid"}},
	} {
		t.Run(tc.subject, func(t *testing.T) {
			nr, es := &fakeNotifRepo{}, &fakeEmailSender{}
			uc := newSendUC(nr, newPrefRepo(), es, fakeContactReader{email: "admin@x.com"})
			err := uc.Execute(context.Background(), SendNotificationCmd{
				TenantID: validTenant(), EventType: tc.subject, TemplateData: tc.data, NatsMsgID: "msg-1",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(es.sent) != 1 || !strings.Contains(es.sent[0].body, tc.wantInBody) {
				t.Fatalf("expected the KYC email to be sent, got %+v", es.sent)
			}
			if len(nr.saved) != 1 || nr.saved[0].EventType() != domain.NormalizeEventType(tc.subject) {
				t.Errorf("saved = %+v", nr.saved)
			}
		})
	}
}

func TestTimestampsComeFromTheInjectedClock(t *testing.T) {
	nr, es := &fakeNotifRepo{}, &fakeEmailSender{}
	uc := newSendUC(nr, newPrefRepo(), es, fakeContactReader{email: "admin@x.com"})
	if err := uc.Execute(context.Background(), SendNotificationCmd{
		TenantID: validTenant(), EventType: "payment.captured.v1", TemplateData: map[string]string{},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n := nr.saved[0]
	if !n.CreatedAt().Equal(testNow) || n.SentAt() == nil || !n.SentAt().Equal(testNow) {
		t.Errorf("created_at=%v sent_at=%v, want %v", n.CreatedAt(), n.SentAt(), testNow)
	}

	pr := newPrefRepo()
	if err := NewUpdatePreferenceUseCase(pr, fixedClock{}).Execute(context.Background(), UpdatePreferenceCmd{
		TenantID: validTenant(), EventType: "payment.captured", Enabled: false,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range pr.upserted {
		if !p.UpdatedAt().Equal(testNow) {
			t.Errorf("updated_at = %v, want %v", p.UpdatedAt(), testNow)
		}
	}
	if len(pr.upserted) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(pr.upserted))
	}
}
