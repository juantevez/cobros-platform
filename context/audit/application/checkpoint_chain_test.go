package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

func TestChainCheckpointer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	head := withIDs(validChain(3), 1)[2]

	t.Run("saves the head when the chain advanced", func(t *testing.T) {
		cps := &fakeCheckpoints{}
		c := NewChainCheckpointer(&fakeRepo{last: head}, cps, newClock(), time.Hour, logger)
		cp, err := c.Checkpoint(context.Background())
		if err != nil || cp == nil {
			t.Fatalf("got %+v, %v", cp, err)
		}
		if cp.EntryID != 3 || cp.EntryHashHex() != head.HashHex() || !cp.CreatedAt.Equal(testNow) {
			t.Errorf("checkpoint = %+v", cp)
		}
		if len(cps.saved) != 1 {
			t.Errorf("saved = %d", len(cps.saved))
		}
	})

	t.Run("no new checkpoint if the head did not move", func(t *testing.T) {
		cps := &fakeCheckpoints{saved: []domain.ChainCheckpoint{{EntryID: 3, EntryHash: head.Hash()}}}
		cp, err := NewChainCheckpointer(&fakeRepo{last: head}, cps, newClock(), time.Hour, logger).Checkpoint(context.Background())
		if err != nil || cp != nil || len(cps.saved) != 1 {
			t.Errorf("cp = %+v, err = %v, saved = %d", cp, err, len(cps.saved))
		}
	})

	t.Run("empty chain: nothing to checkpoint", func(t *testing.T) {
		cps := &fakeCheckpoints{}
		cp, err := NewChainCheckpointer(&fakeRepo{}, cps, newClock(), time.Hour, logger).Checkpoint(context.Background())
		if err != nil || cp != nil || len(cps.saved) != 0 {
			t.Errorf("cp = %+v, err = %v", cp, err)
		}
	})

	t.Run("errors propagated", func(t *testing.T) {
		for name, c := range map[string]*ChainCheckpointer{
			"head":   NewChainCheckpointer(&fakeRepo{findLastErr: errBoom}, &fakeCheckpoints{}, newClock(), 0, logger),
			"latest": NewChainCheckpointer(&fakeRepo{last: head}, &fakeCheckpoints{latestErr: errBoom}, newClock(), 0, logger),
			"save":   NewChainCheckpointer(&fakeRepo{last: head}, &fakeCheckpoints{saveErr: errBoom}, newClock(), 0, logger),
		} {
			if _, err := c.Checkpoint(context.Background()); err == nil {
				t.Errorf("%s: expected error", name)
			}
		}
	})

	t.Run("default interval", func(t *testing.T) {
		if c := NewChainCheckpointer(&fakeRepo{}, &fakeCheckpoints{}, newClock(), 0, logger); c.interval != DefaultCheckpointInterval {
			t.Errorf("interval = %v", c.interval)
		}
	})

	t.Run("Start checkpoints once and stops with the context", func(t *testing.T) {
		cps := &fakeCheckpoints{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			_ = NewChainCheckpointer(&fakeRepo{last: head}, cps, newClock(), time.Hour, logger).Start(ctx)
			close(done)
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		<-done
		if len(cps.saved) != 1 {
			t.Errorf("expected the startup checkpoint, got %d", len(cps.saved))
		}
	})
}
