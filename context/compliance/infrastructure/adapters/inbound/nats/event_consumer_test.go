package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeWatchlist struct {
	matches []domain.Match
	err     error
}

func (w *fakeWatchlist) Screen(ctx context.Context, normalizedName string) ([]domain.Match, error) {
	return w.matches, w.err
}
func (w *fakeWatchlist) Add(ctx context.Context, e domain.WatchlistEntry, n string, at time.Time) error {
	return nil
}
func (w *fakeWatchlist) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	return nil, nil
}

type fakeAlertRepo struct {
	saved   []*domain.Alert
	saveErr error
}

func (r *fakeAlertRepo) Save(ctx context.Context, a *domain.Alert) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, a)
	return nil
}
func (r *fakeAlertRepo) Update(ctx context.Context, a *domain.Alert) error { return nil }
func (r *fakeAlertRepo) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	return nil, domain.ErrAlertNotFound
}
func (r *fakeAlertRepo) ListByTenant(ctx context.Context, t domain.TenantID, s string, l int) ([]*domain.Alert, error) {
	return nil, nil
}

type fakeTxReader struct {
	count int
	err   error
}

func (r *fakeTxReader) CountCapturedSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	return r.count, r.err
}

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }

// fakeConsumer captura la config de arranque.
type fakeConsumer struct {
	cfg     eventbus.ConsumerConfig
	started bool
}

func (c *fakeConsumer) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	c.cfg = cfg
	c.started = true
	return nil
}

func newConsumer(w *fakeWatchlist, r *fakeAlertRepo, tr *fakeTxReader) (*EventConsumer, *fakeConsumer) {
	fc := &fakeConsumer{}
	pub := &fakePublisher{}
	screen := application.NewScreenApplicationUseCase(r, w, fakeTx{}, pub, fixedClock{})
	monitor := application.NewMonitorTransactionUseCase(r, tr, fakeTx{}, pub, fixedClock{}, application.DefaultMonitoringRules())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewEventConsumer(fc, screen, monitor, logger), fc
}

// ── Start*Consumer ────────────────────────────────────────────────────────────

func TestStartConsumers(t *testing.T) {
	t.Run("onboarding config", func(t *testing.T) {
		c, fc := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		if err := c.StartOnboardingConsumer(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !fc.started || fc.cfg.Stream != "ONBOARDING" || fc.cfg.FilterSubject != "onboarding.application.submitted.>" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
		if fc.cfg.Name != "compliance-onboarding-consumer" || fc.cfg.MaxDeliver != 5 {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
	})

	t.Run("payment config", func(t *testing.T) {
		c, fc := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		if err := c.StartPaymentConsumer(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fc.cfg.Stream != "PAYMENT" || fc.cfg.FilterSubject != "payment.captured.>" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
		if fc.cfg.Name != "compliance-payment-consumer" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
	})
}

// ── handleOnboarding ──────────────────────────────────────────────────────────

func TestHandleOnboarding(t *testing.T) {
	tid := uuid.NewString()

	t.Run("match triggers screening and raises alert", func(t *testing.T) {
		w := &fakeWatchlist{matches: []domain.Match{{
			Entry: domain.WatchlistEntry{FullName: "Osama Bin Laden", ListType: "sanctions"},
			Score: 95,
		}}}
		r := &fakeAlertRepo{}
		c, _ := newConsumer(w, r, &fakeTxReader{})
		msg := &eventbus.Message{
			Subject: "onboarding.application.submitted.v1",
			Payload: []byte(`{"application_id":"app-1","tenant_id":"` + tid + `","legal_name":"Osama Bin Laden"}`),
		}
		if err := c.handleOnboarding(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 1 {
			t.Errorf("expected 1 alert raised, got %d", len(r.saved))
		}
	})

	t.Run("malformed payload is acked", func(t *testing.T) {
		c, _ := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		msg := &eventbus.Message{Subject: "x", Payload: []byte(`{bad`)}
		if err := c.handleOnboarding(context.Background(), msg); err != nil {
			t.Fatalf("malformed payload should be acked, got %v", err)
		}
	})

	t.Run("missing tenant is acked", func(t *testing.T) {
		c, _ := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		msg := &eventbus.Message{Payload: []byte(`{"legal_name":"x"}`)}
		if err := c.handleOnboarding(context.Background(), msg); err != nil {
			t.Fatalf("missing tenant should be acked, got %v", err)
		}
	})

	t.Run("screening error triggers nak", func(t *testing.T) {
		w := &fakeWatchlist{err: errors.New("boom")}
		c, _ := newConsumer(w, &fakeAlertRepo{}, &fakeTxReader{})
		msg := &eventbus.Message{
			Payload: []byte(`{"tenant_id":"` + tid + `","legal_name":"x"}`),
		}
		if err := c.handleOnboarding(context.Background(), msg); err == nil {
			t.Fatal("expected error for nak/retry")
		}
	})
}

// ── handlePayment ─────────────────────────────────────────────────────────────

func TestHandlePayment(t *testing.T) {
	tid := uuid.NewString()

	t.Run("large amount raises threshold alert", func(t *testing.T) {
		r := &fakeAlertRepo{}
		c, _ := newConsumer(&fakeWatchlist{}, r, &fakeTxReader{count: 0})
		msg := &eventbus.Message{
			Subject: "payment.captured.v1",
			Payload: []byte(`{"payment_id":"pay-1","tenant_id":"` + tid + `","amount":5000000,"currency":"ARS","payment_method":"card"}`),
		}
		if err := c.handlePayment(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 1 {
			t.Errorf("expected 1 alert, got %d", len(r.saved))
		}
	})

	t.Run("malformed payload is acked", func(t *testing.T) {
		c, _ := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		msg := &eventbus.Message{Payload: []byte(`{bad`)}
		if err := c.handlePayment(context.Background(), msg); err != nil {
			t.Fatalf("malformed payload should be acked, got %v", err)
		}
	})

	t.Run("missing tenant is acked", func(t *testing.T) {
		c, _ := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, &fakeTxReader{})
		msg := &eventbus.Message{Payload: []byte(`{"payment_id":"p","amount":1}`)}
		if err := c.handlePayment(context.Background(), msg); err != nil {
			t.Fatalf("missing tenant should be acked, got %v", err)
		}
	})

	t.Run("monitoring error triggers nak", func(t *testing.T) {
		tr := &fakeTxReader{err: errors.New("boom")}
		c, _ := newConsumer(&fakeWatchlist{}, &fakeAlertRepo{}, tr)
		msg := &eventbus.Message{
			Payload: []byte(`{"payment_id":"p","tenant_id":"` + tid + `","amount":1}`),
		}
		if err := c.handlePayment(context.Background(), msg); err == nil {
			t.Fatal("expected error for nak/retry")
		}
	})
}
