package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestExpiryPoller_runCycle(t *testing.T) {
	past := testNow.Add(-time.Hour)

	// overdueDispute crea una disputa abierta con deadline vencido.
	overdueDispute := func() *domain.Dispute {
		d, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID("t"), "pay-1", "psp", 5000, "ARS",
			domain.ReasonGeneral, past)
		d.PullEvents()
		return d
	}

	t.Run("expires overdue disputes and publishes", func(t *testing.T) {
		r := newRepo()
		r.overdue = []*domain.Dispute{overdueDispute(), overdueDispute()}
		p := &fakePublisher{}
		poller := NewExpiryPoller(r, fakeTx{}, p, newClock(), discardLogger())

		poller.runCycle(context.Background())

		if len(r.updated) != 2 {
			t.Fatalf("expected 2 updated, got %d", len(r.updated))
		}
		for _, d := range r.updated {
			if d.Status() != domain.StatusExpired {
				t.Errorf("expected expired, got %q", d.Status())
			}
		}
		if len(p.published) != 2 {
			t.Errorf("expected 2 published events, got %d", len(p.published))
		}
	})

	t.Run("empty overdue list is a no-op", func(t *testing.T) {
		r := newRepo()
		p := &fakePublisher{}
		poller := NewExpiryPoller(r, fakeTx{}, p, newClock(), discardLogger())
		poller.runCycle(context.Background())
		if len(r.updated) != 0 || len(p.published) != 0 {
			t.Error("nothing should happen for empty overdue list")
		}
	})

	t.Run("list error logged and returns", func(t *testing.T) {
		r := newRepo()
		r.overdueErr = errBoom
		poller := NewExpiryPoller(r, fakeTx{}, &fakePublisher{}, newClock(), discardLogger())
		poller.runCycle(context.Background()) // no debe panickear
		if len(r.updated) != 0 {
			t.Error("nothing should be updated on list error")
		}
	})

	t.Run("skips disputes that cannot expire", func(t *testing.T) {
		r := newRepo()
		// Una disputa ya aceptada no puede expirar; Expire() falla y se saltea.
		accepted, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID("t"), "pay-2", "psp", 5000, "ARS",
			domain.ReasonGeneral, past)
		_ = accepted.Accept("")
		accepted.PullEvents()
		r.overdue = []*domain.Dispute{accepted}
		poller := NewExpiryPoller(r, fakeTx{}, &fakePublisher{}, newClock(), discardLogger())

		poller.runCycle(context.Background())
		if len(r.updated) != 0 {
			t.Error("accepted dispute should be skipped, not updated")
		}
	})

	t.Run("update error is logged, does not abort cycle", func(t *testing.T) {
		r := newRepo()
		r.overdue = []*domain.Dispute{overdueDispute(), overdueDispute()}
		r.updateErr = errBoom
		poller := NewExpiryPoller(r, fakeTx{}, &fakePublisher{}, newClock(), discardLogger())
		poller.runCycle(context.Background()) // ambos fallan al actualizar, sin panic
	})
}

func TestExpiryPoller_Start_cancelledContext(t *testing.T) {
	poller := NewExpiryPoller(newRepo(), fakeTx{}, &fakePublisher{}, newClock(), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ya cancelado: Start debe retornar de inmediato

	done := make(chan error, 1)
	go func() { done <- poller.Start(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return on cancelled context")
	}
}
