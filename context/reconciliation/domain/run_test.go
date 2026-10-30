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
	// testNow en otra zona: el dominio debe normalizar a UTC.
	testNow = time.Date(2026, 10, 2, 6, 30, 0, 0, time.FixedZone("ART", -3*3600))
)

func newTestRun(t *testing.T) *ReconciliationRun {
	t.Helper()
	r, err := NewReconciliationRun(NewRunID(), testTenant, TypePayment, testFrom, testTo, testNow)
	if err != nil {
		t.Fatalf("NewReconciliationRun: %v", err)
	}
	return r
}

// runWithStatus reconstruye un run en st, tomado a testNow (vigente).
func runWithStatus(st RunStatus) *ReconciliationRun {
	return ReconstituteRun(NewRunID(), testTenant, TypePayment, st, testFrom, testTo, 0, 0, 0, "", testFrom, nil, &testNow, 0)
}

func runningSince(startedAt *time.Time) *ReconciliationRun {
	return ReconstituteRun(NewRunID(), testTenant, TypePayment, RunStatusRunning, testFrom, testTo, 0, 0, 0, "", testFrom, nil, startedAt, 0)
}

func TestNewReconciliationRun(t *testing.T) {
	t.Run("creates pending run", func(t *testing.T) {
		id := NewRunID()

		r, err := NewReconciliationRun(id, testTenant, TypeInternal, testFrom, testTo, testNow)
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
		if !r.CreatedAt().Equal(testNow) || r.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", r.CreatedAt())
		}
		if len(r.PullEvents()) != 0 {
			t.Error("creation should not emit events")
		}
	})

	t.Run("global run has empty tenant", func(t *testing.T) {
		r, err := NewReconciliationRun(NewRunID(), "", TypeInternal, testFrom, testTo, testNow)
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
			r, err := NewReconciliationRun(NewRunID(), testTenant, TypePayment, tc.from, tc.to, testNow)
			if !errors.Is(err, ErrInvalidPeriod) || r != nil {
				t.Fatalf("expected ErrInvalidPeriod and nil run, got %v, %v", r, err)
			}
		})
	}
}

func TestReconciliationRun_Start(t *testing.T) {
	t.Run("from pending", func(t *testing.T) {
		r := newTestRun(t)
		if err := r.Start(testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Status() != RunStatusRunning {
			t.Errorf("status = %q, want running", r.Status())
		}
		if r.StartedAt() == nil || !r.StartedAt().Equal(testNow) || r.StartedAt().Location() != time.UTC {
			t.Errorf("startedAt = %v, want %v in UTC", r.StartedAt(), testNow)
		}
		if len(r.PullEvents()) != 0 {
			t.Error("Start should not emit events")
		}
	})

	t.Run("abandoned running run can be taken over", func(t *testing.T) {
		old := testNow.Add(-RunStaleAfter)
		for name, r := range map[string]*ReconciliationRun{
			"lease expired":          runningSince(&old),
			"no record of the claim": runningSince(nil),
		} {
			if !r.IsStale(testNow) {
				t.Errorf("%s: should be stale", name)
			}
			if err := r.Start(testNow); err != nil {
				t.Fatalf("%s: unexpected error: %v", name, err)
			}
			if !r.StartedAt().Equal(testNow) || r.IsStale(testNow) {
				t.Errorf("%s: claim not renewed: startedAt=%v", name, r.StartedAt())
			}
		}
	})

	t.Run("running run within its lease is not stale", func(t *testing.T) {
		recent := testNow.Add(-RunStaleAfter + time.Second)
		r := runningSince(&recent)
		if r.IsStale(testNow) {
			t.Error("should not be stale")
		}
		if err := r.Start(testNow); !errors.Is(err, ErrRunAlreadyRunning) {
			t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
		}
	})

	t.Run("only running runs can be stale", func(t *testing.T) {
		for _, st := range []RunStatus{RunStatusPending, RunStatusCompleted, RunStatusFailed} {
			r := ReconstituteRun(NewRunID(), testTenant, TypePayment, st, testFrom, testTo, 0, 0, 0, "", testFrom, nil, nil, 0)
			if r.IsStale(testNow.Add(24 * time.Hour)) {
				t.Errorf("%s run reported as stale", st)
			}
		}
	})

	t.Run("from failed clears the previous failure", func(t *testing.T) {
		r := ReconstituteRun(NewRunID(), testTenant, TypePayment, RunStatusFailed, testFrom, testTo, 0, 0, 0, "boom", testFrom, &testTo, nil, 0)
		if err := r.Start(testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Status() != RunStatusRunning || r.ErrorMsg() != "" || r.CompletedAt() != nil {
			t.Errorf("status=%q errorMsg=%q completedAt=%v", r.Status(), r.ErrorMsg(), r.CompletedAt())
		}
	})

	for _, st := range []RunStatus{RunStatusRunning, RunStatusCompleted} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			r := runWithStatus(st)
			if err := r.Start(testNow); !errors.Is(err, ErrRunAlreadyRunning) {
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
		_ = r.Start(testNow)

		if err := r.Complete(10, 7, 3, testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if r.Status() != RunStatusCompleted {
			t.Errorf("status = %q, want completed", r.Status())
		}
		if r.TotalRecords() != 10 || r.MatchedCount() != 7 || r.DiscrepancyCount() != 3 {
			t.Errorf("counts = %d/%d/%d", r.TotalRecords(), r.MatchedCount(), r.DiscrepancyCount())
		}
		if r.CompletedAt() == nil || !r.CompletedAt().Equal(testNow) || r.CompletedAt().Location() != time.UTC {
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
		if ev.EventTenantID() != testTenant.String() || !ev.OccurredAt().Equal(testNow) {
			t.Errorf("event tenant=%q occurredAt=%v", ev.EventTenantID(), ev.OccurredAt())
		}
	})

	t.Run("global run emits event with empty tenant", func(t *testing.T) {
		r, _ := NewReconciliationRun(NewRunID(), "", TypeInternal, testFrom, testTo, testNow)
		_ = r.Start(testNow)
		_ = r.Complete(1, 1, 0, testNow)

		ev := r.PullEvents()[0].(ReconciliationCompletedEvent)
		if ev.TenantID != "" || ev.EventTenantID() != "" || ev.Type != "internal_ledger" {
			t.Errorf("event = %+v", ev)
		}
	})

	for _, st := range []RunStatus{RunStatusPending, RunStatusCompleted, RunStatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			r := runWithStatus(st)
			if err := r.Complete(1, 1, 0, testNow); !errors.Is(err, ErrRunNotRunning) {
				t.Fatalf("expected ErrRunNotRunning, got %v", err)
			}
			if r.Status() != st || r.TotalRecords() != 0 || len(r.PullEvents()) != 0 {
				t.Error("rejected Complete must not change the run")
			}
		})
	}
}

func TestReconciliationRun_Fail(t *testing.T) {
	t.Run("records the reason, clears results and emits failed event", func(t *testing.T) {
		r := newTestRun(t)
		_ = r.Start(testNow)

		if err := r.Fail("read payments: boom", testNow); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if r.Status() != RunStatusFailed || r.ErrorMsg() != "read payments: boom" {
			t.Errorf("status=%q errorMsg=%q", r.Status(), r.ErrorMsg())
		}
		if r.TotalRecords() != 0 || r.MatchedCount() != 0 || r.DiscrepancyCount() != 0 {
			t.Errorf("counters = %d/%d/%d, want zero", r.TotalRecords(), r.MatchedCount(), r.DiscrepancyCount())
		}
		if r.CompletedAt() == nil || !r.CompletedAt().Equal(testNow) {
			t.Errorf("completedAt = %v", r.CompletedAt())
		}

		evs := r.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev := evs[0].(ReconciliationFailedEvent)
		want := ReconciliationFailedEvent{baseEvent: ev.baseEvent, RunID: r.ID().String(), TenantID: testTenant.String(), Type: "payment", Error: "read payments: boom"}
		if ev != want || ev.EventType() != "reconciliation.run.failed.v1" || !ev.OccurredAt().Equal(testNow) {
			t.Errorf("event = %+v", ev)
		}
	})

	for _, st := range []RunStatus{RunStatusPending, RunStatusCompleted, RunStatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			r := runWithStatus(st)
			if err := r.Fail("x", testNow); !errors.Is(err, ErrRunNotRunning) {
				t.Fatalf("expected ErrRunNotRunning, got %v", err)
			}
			if r.Status() != st || r.ErrorMsg() != "" || len(r.PullEvents()) != 0 {
				t.Error("rejected Fail must not change the run")
			}
		})
	}
}

func TestReconciliationRun_PullEvents(t *testing.T) {
	r := newTestRun(t)
	_ = r.Start(testNow)
	_ = r.Complete(1, 1, 0, testNow)

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

	started := created.Add(time.Minute)
	r := ReconstituteRun(id, testTenant, TypeInternal, RunStatusFailed, testFrom, testTo, 5, 4, 1, "boom", created, &completed, &started, 7)

	if r.ID() != id || r.TenantID() != testTenant || r.Type() != TypeInternal || r.Status() != RunStatusFailed {
		t.Error("identity/status not restored")
	}
	if !r.PeriodFrom().Equal(testFrom) || !r.PeriodTo().Equal(testTo) {
		t.Error("period not restored")
	}
	if r.TotalRecords() != 5 || r.MatchedCount() != 4 || r.DiscrepancyCount() != 1 || r.ErrorMsg() != "boom" {
		t.Error("results not restored")
	}
	if !r.CreatedAt().Equal(created) || r.CompletedAt() != &completed || r.StartedAt() != &started {
		t.Error("timestamps not restored")
	}
	if r.Version() != 7 {
		t.Errorf("version = %d, want 7", r.Version())
	}
	r.MarkVersionPersisted()
	if r.Version() != 8 {
		t.Errorf("version after persist = %d, want 8", r.Version())
	}
	if len(r.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}
