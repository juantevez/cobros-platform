package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

var errBoom = errors.New("boom")

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeWriter struct {
	payments  []domain.PaymentFact
	movements []domain.LedgerMovement
	err       error
}

func (w *fakeWriter) SavePaymentFact(ctx context.Context, f domain.PaymentFact) error {
	if w.err != nil {
		return w.err
	}
	w.payments = append(w.payments, f)
	return nil
}

func (w *fakeWriter) SaveLedgerMovement(ctx context.Context, m domain.LedgerMovement) error {
	if w.err != nil {
		return w.err
	}
	w.movements = append(w.movements, m)
	return nil
}

type fakeAccounts struct{ types map[string]string }

func (a fakeAccounts) AccountType(ctx context.Context, tenantID, id string) (string, error) {
	return a.types[id], nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return testNow }

// fakeBus captura la configuración y el handler registrados en Start.
type fakeBus struct {
	cfg     eventbus.ConsumerConfig
	handler eventbus.Handler
	err     error
}

func (b *fakeBus) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	b.cfg, b.handler = cfg, h
	return b.err
}

func newConsumer(bus eventbus.Consumer, w *fakeWriter) *EventConsumer {
	project := application.NewProjectEventsUseCase(w, fakeAccounts{types: map[string]string{"acc-1": "merchant_balance"}}, fixedClock{})
	return NewEventConsumer(bus, project, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func msg(subject, payload string) *eventbus.Message {
	return &eventbus.Message{Subject: subject, ID: "evt-1", Payload: []byte(payload)}
}

func withOccurredAt(m *eventbus.Message, v string) *eventbus.Message {
	m.Headers = map[string]string{eventbus.HeaderOccurredAt: v}
	return m
}

// ── Start ─────────────────────────────────────────────────────────────────────

func TestStartConsumers(t *testing.T) {
	cases := []struct {
		name  string
		start func(*EventConsumer, context.Context) error
		want  eventbus.ConsumerConfig
	}{
		{"payment", (*EventConsumer).StartPaymentConsumer, eventbus.ConsumerConfig{
			Stream: "PAYMENT", Name: "reporting-payment-consumer", FilterSubject: "payment.captured.>", MaxDeliver: 5,
		}},
		{"ledger", (*EventConsumer).StartLedgerConsumer, eventbus.ConsumerConfig{
			Stream: "LEDGER", Name: "reporting-ledger-consumer", FilterSubject: "ledger.entry.>", MaxDeliver: 5,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{err: errBoom}
			err := tc.start(newConsumer(bus, &fakeWriter{}), context.Background())

			if !errors.Is(err, errBoom) {
				t.Errorf("Start error should be propagated, got %v", err)
			}
			if bus.cfg != tc.want {
				t.Errorf("config = %+v\nwant %+v", bus.cfg, tc.want)
			}
			if bus.handler == nil {
				t.Error("handler not registered")
			}
		})
	}
}

// ── payment.captured.v1 ───────────────────────────────────────────────────────

func TestHandle_PaymentCaptured(t *testing.T) {
	ctx := context.Background()

	t.Run("projects payment fact", func(t *testing.T) {
		w := &fakeWriter{}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("payment.captured.v1",
			`{"payment_id":"pay-1","tenant_id":"t-1","amount":10000,"currency":"ARS","platform_fee":300,"psp_fee":150,"payment_method":"card","psp_reference":"ignored"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := domain.PaymentFact{
			PaymentID: "pay-1", TenantID: "t-1", Currency: "ARS", Amount: 10000,
			PlatformFee: 300, PSPFee: 150, PaymentMethod: "card", CapturedAt: testNow,
		}
		if len(w.payments) != 1 || w.payments[0] != want {
			t.Errorf("payments = %+v\nwant [%+v]", w.payments, want)
		}
	})

	t.Run("dates fact with occurred-at header", func(t *testing.T) {
		w := &fakeWriter{}
		m := withOccurredAt(msg("payment.captured.v1", `{"payment_id":"pay-1","tenant_id":"t-1"}`), "2026-09-30T23:59:59.5Z")

		if err := newConsumer(&fakeBus{}, w).handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 9, 30, 23, 59, 59, 500_000_000, time.UTC)
		if got := w.payments[0].CapturedAt; !got.Equal(want) {
			t.Errorf("CapturedAt = %v, want %v", got, want)
		}
	})

	for _, header := range []string{"", "yesterday", "2026-09-30"} {
		t.Run("falls back to clock on header "+header, func(t *testing.T) {
			w := &fakeWriter{}
			m := withOccurredAt(msg("payment.captured.v1", `{"payment_id":"pay-1","tenant_id":"t-1"}`), header)

			if err := newConsumer(&fakeBus{}, w).handle(ctx, m); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !w.payments[0].CapturedAt.Equal(testNow) {
				t.Errorf("CapturedAt = %v, want clock %v", w.payments[0].CapturedAt, testNow)
			}
		})
	}

	t.Run("event with tenant but no payment id is acked (no retry)", func(t *testing.T) {
		w := &fakeWriter{}
		if err := newConsumer(&fakeBus{}, w).handle(ctx, msg("payment.captured.v1", `{"tenant_id":"t-1","amount":100}`)); err != nil {
			t.Fatalf("invalid fact should be acked, got %v", err)
		}
		if len(w.payments) != 0 {
			t.Error("nothing should be projected")
		}
	})

	for _, payload := range []string{`{not json`, `{"payment_id":"pay-1"}`, `{"payment_id":"pay-1","tenant_id":""}`} {
		t.Run("acks without projecting: "+payload, func(t *testing.T) {
			w := &fakeWriter{}
			if err := newConsumer(&fakeBus{}, w).handle(ctx, msg("payment.captured.v1", payload)); err != nil {
				t.Fatalf("malformed payload should be acked, got %v", err)
			}
			if len(w.payments) != 0 {
				t.Error("nothing should be projected")
			}
		})
	}

	t.Run("projection error naks for retry", func(t *testing.T) {
		w := &fakeWriter{err: errBoom}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("payment.captured.v1", `{"payment_id":"pay-1","tenant_id":"t-1"}`))
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── ledger.entry.posted.v1 ────────────────────────────────────────────────────

func TestHandle_EntryPosted(t *testing.T) {
	ctx := context.Background()

	t.Run("projects one movement per posting", func(t *testing.T) {
		w := &fakeWriter{}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.posted.v1",
			`{"entry_id":"e-1","tenant_id":"t-1","postings":[`+
				`{"account_id":"acc-1","direction":"credit","amount":500,"currency":"ARS"},`+
				`{"account_id":"acc-2","direction":"debit","amount":500,"currency":"ARS"}]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.LedgerMovement{
			{EntryID: "e-1", AccountID: "acc-1", Direction: "credit", TenantID: "t-1", AccountType: "merchant_balance", Currency: "ARS", Amount: 500, PostedAt: testNow},
			{EntryID: "e-1", AccountID: "acc-2", Direction: "debit", TenantID: "t-1", AccountType: "", Currency: "ARS", Amount: 500, PostedAt: testNow},
		}
		if !reflect.DeepEqual(w.movements, want) {
			t.Errorf("movements = %+v\nwant %+v", w.movements, want)
		}
	})

	t.Run("dates movements with occurred-at header", func(t *testing.T) {
		w := &fakeWriter{}
		m := withOccurredAt(msg("ledger.entry.posted.v1",
			`{"entry_id":"e-1","tenant_id":"t-1","postings":[{"account_id":"acc-1","direction":"credit","amount":1,"currency":"ARS"}]}`),
			"2026-09-15T10:00:00Z")

		if err := newConsumer(&fakeBus{}, w).handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC); !w.movements[0].PostedAt.Equal(want) {
			t.Errorf("PostedAt = %v, want %v", w.movements[0].PostedAt, want)
		}
	})

	t.Run("event with tenant but no entry id is acked (no retry)", func(t *testing.T) {
		w := &fakeWriter{}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.posted.v1",
			`{"tenant_id":"t-1","postings":[{"account_id":"acc-1","direction":"credit","amount":1,"currency":"ARS"}]}`))
		if err != nil {
			t.Fatalf("invalid fact should be acked, got %v", err)
		}
		if len(w.movements) != 0 {
			t.Error("nothing should be projected")
		}
	})

	for _, payload := range []string{`[]`, `{"entry_id":"e-1","postings":[]}`} {
		t.Run("acks without projecting: "+payload, func(t *testing.T) {
			w := &fakeWriter{}
			if err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.posted.v1", payload)); err != nil {
				t.Fatalf("malformed payload should be acked, got %v", err)
			}
			if len(w.movements) != 0 {
				t.Error("nothing should be projected")
			}
		})
	}

	t.Run("projection error naks for retry", func(t *testing.T) {
		w := &fakeWriter{err: errBoom}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.posted.v1",
			`{"entry_id":"e-1","tenant_id":"t-1","postings":[{"account_id":"acc-1","direction":"credit","amount":1,"currency":"ARS"}]}`))
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestHandle_EntryReversed(t *testing.T) {
	ctx := context.Background()
	reversal := `{"reverse_entry_id":"r-1","original_entry_id":"e-1","tenant_id":"t-1","postings":[` +
		`{"account_id":"acc-1","direction":"debit","amount":500,"currency":"ARS"},` +
		`{"account_id":"acc-2","direction":"credit","amount":500,"currency":"ARS"}]}`

	t.Run("projects the reversal postings under the reverse entry", func(t *testing.T) {
		w := &fakeWriter{}
		m := withOccurredAt(msg("ledger.entry.reversed.v1", reversal), "2026-09-20T10:00:00Z")
		if err := newConsumer(&fakeBus{}, w).handle(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
		want := []domain.LedgerMovement{
			{EntryID: "r-1", AccountID: "acc-1", Direction: "debit", TenantID: "t-1", AccountType: "merchant_balance", Currency: "ARS", Amount: 500, PostedAt: at},
			{EntryID: "r-1", AccountID: "acc-2", Direction: "credit", TenantID: "t-1", AccountType: "", Currency: "ARS", Amount: 500, PostedAt: at},
		}
		if !reflect.DeepEqual(w.movements, want) {
			t.Errorf("movements = %+v\nwant %+v", w.movements, want)
		}
	})

	t.Run("reversal without postings is acked without projecting", func(t *testing.T) {
		w := &fakeWriter{err: errBoom}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.reversed.v1",
			`{"reverse_entry_id":"r-1","original_entry_id":"e-1","tenant_id":"t-1"}`))
		if err != nil {
			t.Fatalf("expected ack, got %v", err)
		}
	})

	for _, payload := range []string{`[]`, `{"reverse_entry_id":"r-1","postings":[]}`} {
		t.Run("acks malformed payload: "+payload, func(t *testing.T) {
			w := &fakeWriter{err: errBoom}
			if err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.reversed.v1", payload)); err != nil {
				t.Fatalf("expected ack, got %v", err)
			}
		})
	}

	t.Run("reversal without reverse id is acked as invalid fact", func(t *testing.T) {
		w := &fakeWriter{}
		err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.reversed.v1",
			`{"tenant_id":"t-1","postings":[{"account_id":"acc-1","direction":"debit","amount":1,"currency":"ARS"}]}`))
		if err != nil || len(w.movements) != 0 {
			t.Fatalf("expected ack without projection, got %v (%d)", err, len(w.movements))
		}
	})

	t.Run("projection error naks for retry", func(t *testing.T) {
		w := &fakeWriter{err: errBoom}
		if err := newConsumer(&fakeBus{}, w).handle(ctx, msg("ledger.entry.reversed.v1", reversal)); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestHandle_IgnoresOtherSubjects(t *testing.T) {
	w := &fakeWriter{err: errBoom} // si se proyectara algo, fallaría
	for _, subject := range []string{"payment.failed.v1", "payment.captured.v2", "ledger.entry.reversed.v2", "ledger.account.created.v1", ""} {
		if err := newConsumer(&fakeBus{}, w).handle(context.Background(), msg(subject, `{"tenant_id":"t-1"}`)); err != nil {
			t.Errorf("subject %q should be acked silently, got %v", subject, err)
		}
	}
}
