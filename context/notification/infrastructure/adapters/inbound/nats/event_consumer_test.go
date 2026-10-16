package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/notification/application"
	"github.com/juantevez/cobros-platform/context/notification/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// ── Fakes de puertos del SendNotificationUseCase ──────────────────────────────

type fakeNotifRepo struct{ saved []*domain.Notification }

func (r *fakeNotifRepo) Save(ctx context.Context, n *domain.Notification) error {
	r.saved = append(r.saved, n)
	return nil
}
func (r *fakeNotifRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	return nil, nil
}

type fakePrefRepo struct{}

func (fakePrefRepo) Upsert(ctx context.Context, p *domain.NotificationPreference) error { return nil }
func (fakePrefRepo) FindByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) (*domain.NotificationPreference, error) {
	return nil, domain.ErrPreferenceNotFound
}
func (fakePrefRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	return nil, nil
}

type fakeEmailSender struct {
	sent []string // subjects enviados
	err  error
}

func (s *fakeEmailSender) Send(ctx context.Context, to, subject, body string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, subject)
	return nil
}

type fakeContactReader struct{ email string }

func (c fakeContactReader) GetTenantAdminEmail(ctx context.Context, tenantID string) (string, error) {
	return c.email, nil
}

// fakeConsumer captura la config con la que se lo arranca.
type fakeConsumer struct {
	cfg     eventbus.ConsumerConfig
	started bool
}

func (c *fakeConsumer) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	c.cfg = cfg
	c.started = true
	return nil
}

func newConsumer(es *fakeEmailSender) (*EventConsumer, *fakeConsumer, *fakeNotifRepo) {
	fc := &fakeConsumer{}
	nr := &fakeNotifRepo{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uc := application.NewSendNotificationUseCase(nr, fakePrefRepo{}, es, fakeContactReader{email: "admin@x.com"}, logger)
	return NewEventConsumer(fc, uc, logger), fc, nr
}

// ── buildTemplateData ─────────────────────────────────────────────────────────

func TestBuildTemplateData(t *testing.T) {
	p := eventPayload{
		TenantID:        "ten-1",
		PaymentID:       "pay-1",
		Amount:          150050, // 1500.50
		Currency:        "ARS",
		FailureReason:   "insufficient_funds",
		PaymentMethod:   "card",
		BankReference:   "bank-9",
		RejectionReason: "docs_invalid",
	}

	cases := []struct {
		subject string
		want    map[string]string
	}{
		{"payment.captured.v1", map[string]string{
			"payment_id": "pay-1", "amount": "1500.50", "currency": "ARS", "payment_method": "card",
		}},
		{"payment.failed.v1", map[string]string{
			"payment_id": "pay-1", "amount": "1500.50", "currency": "ARS", "failure_reason": "insufficient_funds",
		}},
		{"payment.risk_rejected.v1", map[string]string{
			"payment_id": "pay-1", "amount": "1500.50", "currency": "ARS", "failure_reason": "insufficient_funds",
		}},
		{"payment.refunded.v1", map[string]string{
			"payment_id": "pay-1", "amount": "1500.50", "currency": "ARS",
		}},
		{"payout.confirmed.v1", map[string]string{
			"amount": "1500.50", "currency": "ARS", "bank_reference": "bank-9",
		}},
		{"payout.failed.v1", map[string]string{
			"amount": "1500.50", "currency": "ARS", "failure_reason": "insufficient_funds",
		}},
		{"onboarding.application.approved.v1", map[string]string{}},
		{"onboarding.application.rejected.v1", map[string]string{
			"rejection_reason": "docs_invalid",
		}},
		{"auth.tenant.suspended.v1", map[string]string{}},
	}

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			got := buildTemplateData(tc.subject, p)
			if got == nil {
				t.Fatalf("expected data for %q, got nil", tc.subject)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("keys = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("data[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}

	t.Run("unknown subject returns nil", func(t *testing.T) {
		if got := buildTemplateData("payment.exploded.v1", p); got != nil {
			t.Errorf("expected nil for unknown subject, got %v", got)
		}
	})

	t.Run("amount formatting rounds to two decimals", func(t *testing.T) {
		got := buildTemplateData("payment.refunded.v1", eventPayload{Amount: 5})
		if got["amount"] != "0.05" {
			t.Errorf("amount = %q, want 0.05", got["amount"])
		}
	})
}

// ── handle ────────────────────────────────────────────────────────────────────

func TestHandle(t *testing.T) {
	validPayload := func() []byte {
		return []byte(`{"tenant_id":"` + uuid.NewString() + `","payment_id":"pay-1","amount":1000,"currency":"ARS"}`)
	}

	t.Run("known event sends notification", func(t *testing.T) {
		es := &fakeEmailSender{}
		c, _, nr := newConsumer(es)
		msg := &eventbus.Message{Subject: "payment.captured.v1", Payload: validPayload()}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 1 {
			t.Fatalf("expected 1 email sent, got %d", len(es.sent))
		}
		if len(nr.saved) != 1 || nr.saved[0].Status() != domain.StatusSent {
			t.Errorf("expected 1 sent record, got %+v", nr.saved)
		}
	})

	t.Run("malformed json is acked and skipped", func(t *testing.T) {
		es := &fakeEmailSender{}
		c, _, _ := newConsumer(es)
		msg := &eventbus.Message{Subject: "payment.captured.v1", Payload: []byte(`{bad`)}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("malformed event should ack (nil), got %v", err)
		}
		if len(es.sent) != 0 {
			t.Error("nothing should be sent for malformed event")
		}
	})

	t.Run("empty tenant is acked and skipped", func(t *testing.T) {
		es := &fakeEmailSender{}
		c, _, _ := newConsumer(es)
		msg := &eventbus.Message{Subject: "payment.captured.v1", Payload: []byte(`{"tenant_id":""}`)}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 0 {
			t.Error("nothing should be sent without tenant")
		}
	})

	t.Run("subject without template is skipped", func(t *testing.T) {
		es := &fakeEmailSender{}
		c, _, _ := newConsumer(es)
		msg := &eventbus.Message{Subject: "payment.exploded.v1", Payload: validPayload()}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(es.sent) != 0 {
			t.Error("nothing should be sent for subject without template")
		}
	})

	t.Run("use case error is logged and acked", func(t *testing.T) {
		// tenant_id no vacío pero inválido → Execute retorna error de parseo,
		// que se loguea y se ackea (no se re-entrega).
		es := &fakeEmailSender{}
		c, _, _ := newConsumer(es)
		msg := &eventbus.Message{
			Subject: "payment.captured.v1",
			Payload: []byte(`{"tenant_id":"not-a-uuid","amount":1000,"currency":"ARS"}`),
		}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("use case error should ack (nil), got %v", err)
		}
		if len(es.sent) != 0 {
			t.Error("nothing should be sent when tenant id is invalid")
		}
	})

	t.Run("send failure is still acked", func(t *testing.T) {
		es := &fakeEmailSender{err: errors.New("smtp down")}
		c, _, _ := newConsumer(es)
		msg := &eventbus.Message{Subject: "payment.captured.v1", Payload: validPayload()}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("send failure should ack (nil), got %v", err)
		}
	})
}

// ── Start*Consumer ────────────────────────────────────────────────────────────

func TestStartConsumers(t *testing.T) {
	cases := []struct {
		name              string
		start             func(*EventConsumer, context.Context) error
		stream, filter    string
		consumerName      string
	}{
		{"payment", (*EventConsumer).StartPaymentConsumer, "PAYMENT", "payment.>", "notification-payment-consumer"},
		{"payout", (*EventConsumer).StartPayoutConsumer, "PAYOUT", "payout.>", "notification-payout-consumer"},
		{"onboarding", (*EventConsumer).StartOnboardingConsumer, "ONBOARDING", "onboarding.application.>", "notification-onboarding-consumer"},
		{"auth", (*EventConsumer).StartAuthConsumer, "AUTH", "auth.tenant.suspended.>", "notification-auth-consumer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, fc, _ := newConsumer(&fakeEmailSender{})
			if err := tc.start(c, context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !fc.started {
				t.Fatal("consumer not started")
			}
			if fc.cfg.Stream != tc.stream || fc.cfg.FilterSubject != tc.filter {
				t.Errorf("unexpected config: %+v", fc.cfg)
			}
			if fc.cfg.Name != tc.consumerName || fc.cfg.MaxDeliver != 3 {
				t.Errorf("unexpected config: %+v", fc.cfg)
			}
		})
	}
}
