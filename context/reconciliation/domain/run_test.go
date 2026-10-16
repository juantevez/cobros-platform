package domain

import (
	"errors"
	"testing"
	"time"
)

const testTenant = TenantID("11111111-1111-1111-1111-111111111111")

var (
	testFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

func newTestRun(t *testing.T) *ReconciliationRun {
	t.Helper()
	r, err := NewReconciliationRun(NewRunID(), testTenant, TypePayment, testFrom, testTo)
	if err != nil {
		t.Fatalf("NewReconciliationRun: %v", err)
	}
	return r
}

func runWithStatus(st RunStatus) *ReconciliationRun {
	return ReconstituteRun(NewRunID(), testTenant, TypePayment, st, testFrom, testTo, 0, 0, 0, "", testFrom, nil)
}

func TestNewReconciliationRun(t *testing.T) {
	t.Run("creates pending run", func(t *testing.T) {
		id := NewRunID()
		before := time.Now().UTC()

		r, err := NewReconciliationRun(id, testTenant, TypeInternal, testFrom, testTo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.ID() != id || r.TenantID() != testTenant || r.Type() != TypeInternal {
			t.Errorf("identity not set: %v %v %v", r.ID(), r.TenantID(), r.Type())
		}
		if r.Status() != RunStatusPending {
			t.Errorf("status = %q, want pending", r.Status())
		}
		if !r.PeriodFrom().Equal(testFrom) || !r.PeriodTo().Equal(testTo) {
			t.Errorf("period = %v..%v", r.PeriodFrom(), r.PeriodTo())
		}
		if r.TotalRecords() != 0 || r.MatchedCount() != 0 || r.DiscrepancyCount() != 0 || r.ErrorMsg() != "" {
			t.Error("counters and error should be empty")
		}
		if r.CompletedAt() != nil {
			t.Error("completedAt should be nil")
		}
		if r.CreatedAt().Before(before) || r.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", r.CreatedAt())
		}
		if len(r.PullEvents()) != 0 {
			t.Error("creation should not emit events")
		}
	})

	t.Run("global run has empty tenant", func(t *testing.T) {
		r, err := NewReconciliationRun(NewRunID(), "", TypeInternal, testFrom, testTo)
		if err != nil || r.TenantID() != "" {
			t.Fatalf("got tenant %q, %v", r.TenantID(), err)
		}
	})

	invalid := []struct {
		name     string
		from, to time.Time
	}{
		{"from equals to", testFrom, testFrom},
		{"from after to", testTo, testFrom},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewReconciliationRun(NewRunID(), testTenant, TypePayment, tc.from, tc.to)
			if !errors.Is(err, ErrInvalidPeriod) || r != nil {
				t.Fatalf("expected ErrInvalidPeriod and nil run, got %v, %v", r, err)
			}
		})
	}
}

func TestReconciliationRun_Start(t *testing.T) {
	t.Run("from pending", func(t *testing.T) {
		r := newTestRun(t)
		if err := r.Start(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Status() != RunStatusRunning {
			t.Errorf("status = %q, want running", r.Status())
		}
		if len(r.PullEvents()) != 0 {
			t.Error("Start should not emit events")
		}
	})

	for _, st := range []RunStatus{RunStatusRunning, RunStatusCompleted, RunStatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			r := runWithStatus(st)
			if err := r.Start(); !errors.Is(err, ErrRunAlreadyRunning) {
				t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
			}
			if r.Status() != st {
				t.Errorf("status changed to %q", r.Status())
			}
		})
	}
}

func TestReconciliationRun_Complete(t *testing.T) {
	t.Run("records results and emits completed event", func(t *testing.T) {
		r := newTestRun(t)
		_ = r.Start()
		before := time.Now().UTC()

		r.Complete(10, 7, 3)

		if r.Status() != RunStatusCompleted {
			t.Errorf("status = %q, want completed", r.Status())
		}
		if r.TotalRecords() != 10 || r.MatchedCount() != 7 || r.DiscrepancyCount() != 3 {
			t.Errorf("counts = %d/%d/%d", r.TotalRecords(), r.MatchedCount(), r.DiscrepancyCount())
		}
		if r.CompletedAt() == nil || r.CompletedAt().Before(before) {
			t.Errorf("completedAt = %v", r.CompletedAt())
		}

		evs := r.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(ReconciliationCompletedEvent)
		if !ok {
			t.Fatalf("expected ReconciliationCompletedEvent, got %T", evs[0])
		}
		want := ReconciliationCompletedEvent{
			baseEvent:        ev.baseEvent,
			RunID:            r.ID().String(),
			TenantID:         testTenant.String(),
			Type:             "payment",
			TotalRecords:     10,
			MatchedCount:     7,
			DiscrepancyCount: 3,
		}
		if ev != want {
			t.Errorf("event = %+v\nwant %+v", ev, want)
		}
		if ev.EventTenantID() != testTenant.String() {
			t.Errorf("event tenant = %q", ev.EventTenantID())
		}
	})

	t.Run("global run emits event with empty tenant", func(t *testing.T) {
		r, _ := NewReconciliationRun(NewRunID(), "", TypeInternal, testFrom, testTo)
		r.Complete(1, 1, 0)

		ev := r.PullEvents()[0].(ReconciliationCompletedEvent)
		if ev.TenantID != "" || ev.EventTenantID() != "" || ev.Type != "internal_ledger" {
			t.Errorf("event = %+v", ev)
		}
	})

	// Complete no valida el estado previo: el caso de uso es responsable de
	// llamarlo sólo sobre runs en running.
	t.Run("does not guard previous status", func(t *testing.T) {
		r := newTestRun(t)
		r.Complete(0, 0, 0)
		if r.Status() != RunStatusCompleted {
			t.Errorf("status = %q, want completed", r.Status())
		}
	})
}

func TestReconciliationRun_Fail(t *testing.T) {
	r := newTestRun(t)
	_ = r.Start()

	r.Fail("parse error")

	if r.Status() != RunStatusFailed || r.ErrorMsg() != "parse error" {
		t.Errorf("status=%q errorMsg=%q", r.Status(), r.ErrorMsg())
	}
	if r.CompletedAt() == nil {
		t.Error("completedAt should be set on failure")
	}
	if len(r.PullEvents()) != 0 {
		t.Error("Fail should not emit events")
	}
}

func TestReconciliationRun_PullEvents(t *testing.T) {
	r := newTestRun(t)
	r.Complete(1, 1, 0)

	if len(r.PullEvents()) != 1 {
		t.Fatal("first pull should return the event")
	}
	if len(r.PullEvents()) != 0 {
		t.Error("second pull should be empty")
	}
}

func TestReconstituteRun(t *testing.T) {
	id := NewRunID()
	created := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	completed := created.Add(time.Hour)

	r := ReconstituteRun(id, testTenant, TypeInternal, RunStatusFailed, testFrom, testTo, 5, 4, 1, "boom", created, &completed)

	if r.ID() != id || r.TenantID() != testTenant || r.Type() != TypeInternal || r.Status() != RunStatusFailed {
		t.Error("identity/status not restored")
	}
	if !r.PeriodFrom().Equal(testFrom) || !r.PeriodTo().Equal(testTo) {
		t.Error("period not restored")
	}
	if r.TotalRecords() != 5 || r.MatchedCount() != 4 || r.DiscrepancyCount() != 1 || r.ErrorMsg() != "boom" {
		t.Error("results not restored")
	}
	if !r.CreatedAt().Equal(created) || r.CompletedAt() != &completed {
		t.Error("timestamps not restored")
	}
	if len(r.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}
