package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestProjectionPurger(t *testing.T) {
	rules := DefaultMonitoringRules()

	t.Run("purges in batches until a partial batch", func(t *testing.T) {
		tl := &fakeTxLog{purgeBatches: []int{purgeBatchSize, purgeBatchSize, 12}}
		p := NewProjectionPurger(tl, newClock(), rules, 48*time.Hour, time.Hour, discardLogger())
		p.runCycle(context.Background())
		if len(tl.purgeBefore) != 3 {
			t.Fatalf("expected 3 batches, got %d", len(tl.purgeBefore))
		}
		if want := testNow.Add(-48 * time.Hour); !tl.purgeBefore[0].Equal(want) {
			t.Errorf("before = %v, want %v", tl.purgeBefore[0], want)
		}
	})

	t.Run("stops on error", func(t *testing.T) {
		tl := &fakeTxLog{purgeErr: errBoom}
		NewProjectionPurger(tl, newClock(), rules, 48*time.Hour, time.Hour, discardLogger()).runCycle(context.Background())
		if len(tl.purgeBefore) != 1 {
			t.Errorf("expected a single attempt, got %d", len(tl.purgeBefore))
		}
	})

	t.Run("retention never shorter than the rules window", func(t *testing.T) {
		tl := &fakeTxLog{}
		p := NewProjectionPurger(tl, newClock(), rules, time.Minute, 0, discardLogger())
		if p.retention != rules.VelocityWindow || p.interval != DefaultPurgeInterval {
			t.Errorf("retention/interval = %v / %v", p.retention, p.interval)
		}
	})

	t.Run("zero retention uses the default", func(t *testing.T) {
		noWindow := rules
		noWindow.VelocityWindow = 0
		p := NewProjectionPurger(&fakeTxLog{}, newClock(), noWindow, 0, 0, discardLogger())
		if p.retention != DefaultProjectionRetention {
			t.Errorf("retention = %v, want default", p.retention)
		}
	})

	t.Run("Start runs a cycle and stops with the context", func(t *testing.T) {
		tl := &fakeTxLog{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			_ = NewProjectionPurger(tl, newClock(), rules, 0, time.Hour, discardLogger()).Start(ctx)
			close(done)
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		<-done
		if len(tl.purgeBefore) != 1 {
			t.Errorf("expected the startup cycle, got %d calls", len(tl.purgeBefore))
		}
	})
}
