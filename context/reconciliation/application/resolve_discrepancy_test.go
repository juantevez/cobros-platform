package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

func TestResolveDiscrepancyUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	actions := []struct {
		action string
		want   domain.DiscrepancyStatus
	}{
		{"resolve", domain.DiscrepancyResolved},
		{"ignore", domain.DiscrepancyIgnored},
	}
	for _, tc := range actions {
		t.Run(tc.action+" open discrepancy", func(t *testing.T) {
			d := openDiscrepancy()
			repo := newDiscRepo(d)
			tx, pub := &fakeTx{}, &fakePublisher{}

			err := NewResolveDiscrepancyUseCase(repo, tx, pub).Execute(ctx, ResolveDiscrepancyCmd{
				TenantID: tenantA, DiscrepancyID: d.ID().String(), Action: tc.action, ResolvedBy: "op-1", Notes: "checked",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Status() != tc.want || d.ResolvedBy() != "op-1" || d.Notes() != "checked" || d.ResolvedAt() == nil {
				t.Errorf("status=%q by=%q notes=%q", d.Status(), d.ResolvedBy(), d.Notes())
			}
			if len(repo.updated) != 1 || repo.updated[0] != d {
				t.Error("discrepancy should be updated once")
			}
			if tx.calls != 1 || len(pub.events) != 1 {
				t.Fatalf("tx calls=%d events=%d, want 1 and 1", tx.calls, len(pub.events))
			}
			ev, ok := pub.events[0].(domain.DiscrepancyResolvedEvent)
			if !ok || ev.DiscrepancyID != d.ID().String() || ev.RunID != d.RunID().String() ||
				ev.TenantID != tenantA || ev.Status != string(tc.want) || ev.ResolvedBy != "op-1" || ev.Type != "amount_mismatch" {
				t.Errorf("event = %+v", pub.events[0])
			}
		})
	}

	t.Run("publish error rolls back the resolution", func(t *testing.T) {
		d := openDiscrepancy()
		tx := &fakeTx{}

		err := NewResolveDiscrepancyUseCase(newDiscRepo(d), tx, &fakePublisher{err: errBoom}).Execute(ctx, ResolveDiscrepancyCmd{
			TenantID: tenantA, DiscrepancyID: d.ID().String(), Action: "resolve", ResolvedBy: "op",
		})
		if !errors.Is(err, errBoom) || tx.rollbacks != 1 {
			t.Fatalf("err=%v rollbacks=%d, want errBoom and 1", err, tx.rollbacks)
		}
	})

	t.Run("missing resolved_by rejected before lookup", func(t *testing.T) {
		repo := newDiscRepo()
		repo.findErr = errBoom

		err := NewResolveDiscrepancyUseCase(repo, &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{DiscrepancyID: "x", Action: "resolve"})
		if err == nil || !strings.Contains(err.Error(), "resolved_by is required") {
			t.Fatalf("expected resolved_by error, got %v", err)
		}
	})

	t.Run("invalid discrepancy id", func(t *testing.T) {
		err := NewResolveDiscrepancyUseCase(newDiscRepo(), &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{TenantID: tenantA, DiscrepancyID: "x", Action: "resolve", ResolvedBy: "op"})
		if err == nil || !strings.Contains(err.Error(), "invalid discrepancy id") {
			t.Fatalf("expected invalid id error, got %v", err)
		}
	})

	t.Run("unknown discrepancy returns ErrDiscrepancyNotFound", func(t *testing.T) {
		err := NewResolveDiscrepancyUseCase(newDiscRepo(), &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{
			TenantID: tenantA, DiscrepancyID: domain.NewDiscrepancyID().String(), Action: "resolve", ResolvedBy: "op",
		})
		if !errors.Is(err, domain.ErrDiscrepancyNotFound) {
			t.Fatalf("expected ErrDiscrepancyNotFound, got %v", err)
		}
	})

	t.Run("discrepancy of another tenant is reported as not found", func(t *testing.T) {
		d := openDiscrepancy()
		repo := newDiscRepo(d)

		err := NewResolveDiscrepancyUseCase(repo, &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{
			TenantID: tenantB, DiscrepancyID: d.ID().String(), Action: "resolve", ResolvedBy: "op",
		})
		if !errors.Is(err, domain.ErrDiscrepancyNotFound) {
			t.Fatalf("expected ErrDiscrepancyNotFound, got %v", err)
		}
		if d.Status() != domain.DiscrepancyOpen || len(repo.updated) != 0 {
			t.Error("foreign discrepancy must not be touched")
		}
	})

	t.Run("invalid action leaves discrepancy open", func(t *testing.T) {
		d := openDiscrepancy()
		repo := newDiscRepo(d)

		err := NewResolveDiscrepancyUseCase(repo, &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{
			TenantID: tenantA, DiscrepancyID: d.ID().String(), Action: "delete", ResolvedBy: "op",
		})
		if err == nil || !strings.Contains(err.Error(), `invalid action "delete"`) {
			t.Fatalf("expected invalid action error, got %v", err)
		}
		if d.Status() != domain.DiscrepancyOpen || len(repo.updated) != 0 {
			t.Error("discrepancy must stay open and not be updated")
		}
	})

	for _, tc := range actions {
		t.Run(tc.action+" already closed discrepancy is rejected", func(t *testing.T) {
			d := openDiscrepancy()
			_ = d.Ignore("someone", "")
			repo := newDiscRepo(d)

			err := NewResolveDiscrepancyUseCase(repo, &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{
				TenantID: tenantA, DiscrepancyID: d.ID().String(), Action: tc.action, ResolvedBy: "op",
			})
			if !errors.Is(err, domain.ErrDiscrepancyAlreadyResolved) {
				t.Fatalf("expected ErrDiscrepancyAlreadyResolved, got %v", err)
			}
			if len(repo.updated) != 0 {
				t.Error("nothing should be updated")
			}
		})
	}

	t.Run("update error is returned", func(t *testing.T) {
		d := openDiscrepancy()
		repo := newDiscRepo(d)
		repo.updateErr = errBoom

		err := NewResolveDiscrepancyUseCase(repo, &fakeTx{}, &fakePublisher{}).Execute(ctx, ResolveDiscrepancyCmd{
			TenantID: tenantA, DiscrepancyID: d.ID().String(), Action: "resolve", ResolvedBy: "op",
		})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestGetReportUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid status filter is rejected", func(t *testing.T) {
		run := pendingRun(tenantA, domain.TypePayment)
		discs := newDiscRepo()
		_, err := NewGetReportUseCase(newRunRepo(run), discs).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: run.ID().String(), StatusFilter: "closed"})
		if !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), `"closed"`) {
			t.Fatalf("expected ErrInvalidArgument, got %v", err)
		}
	})
	created := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	completed := created.Add(time.Hour)

	completedRun := domain.ReconstituteRun(
		domain.NewRunID(), domain.TenantID(tenantA), domain.TypePayment, domain.RunStatusCompleted,
		periodFrom, periodTo, 10, 8, 2, "", created, &completed,
	)

	t.Run("returns run and discrepancy views", func(t *testing.T) {
		runs := newRunRepo(completedRun)
		resolvedAt := completed
		disc := domain.ReconstituteDiscrepancy(
			domain.NewDiscrepancyID(), completedRun.ID(), domain.TenantID(tenantA),
			domain.DiscrepancyStatusMismatch, "PSP-1", "captured", "rejected",
			domain.DiscrepancyResolved, "fixed", "op-1", &resolvedAt, created,
		)
		discs := newDiscRepo()
		discs.list = []*domain.Discrepancy{disc}

		rep, err := NewGetReportUseCase(runs, discs).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: completedRun.ID().String(), StatusFilter: "resolved", Limit: 5})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		completedStr := "2026-10-02T09:00:00Z"
		wantRun := RunView{
			ID:               completedRun.ID().String(),
			TenantID:         tenantA,
			Type:             "payment",
			Status:           "completed",
			PeriodFrom:       periodFrom,
			PeriodTo:         periodTo,
			TotalRecords:     10,
			MatchedCount:     8,
			DiscrepancyCount: 2,
			CreatedAt:        "2026-10-02T08:00:00Z",
			CompletedAt:      &completedStr,
		}
		if !reflect.DeepEqual(rep.Run, wantRun) {
			t.Errorf("run view = %+v\nwant %+v", rep.Run, wantRun)
		}

		wantDisc := DiscrepancyView{
			ID:            disc.ID().String(),
			RunID:         completedRun.ID().String(),
			Type:          "status_mismatch",
			RecordID:      "PSP-1",
			SystemValue:   "captured",
			ExternalValue: "rejected",
			Status:        "resolved",
			Notes:         "fixed",
			ResolvedBy:    "op-1",
			CreatedAt:     "2026-10-02T08:00:00Z",
		}
		if len(rep.Discrepancies) != 1 || rep.Discrepancies[0] != wantDisc {
			t.Errorf("discrepancies = %+v\nwant [%+v]", rep.Discrepancies, wantDisc)
		}

		if discs.lastRunID != completedRun.ID() || discs.lastFilter != "resolved" || discs.lastLimit != 5 {
			t.Errorf("ListByRun called with run=%q filter=%q limit=%d", discs.lastRunID, discs.lastFilter, discs.lastLimit)
		}
	})

	t.Run("pending run has no completed_at and empty discrepancies", func(t *testing.T) {
		run := pendingRun(tenantA, domain.TypePayment)
		rep, err := NewGetReportUseCase(newRunRepo(run), newDiscRepo()).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: run.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rep.Run.CompletedAt != nil || rep.Run.Status != "pending" {
			t.Errorf("run view = %+v", rep.Run)
		}
		if rep.Discrepancies == nil || len(rep.Discrepancies) != 0 {
			t.Errorf("expected empty non-nil discrepancies, got %#v", rep.Discrepancies)
		}
	})

	for _, limit := range []int{0, -1} {
		t.Run("non-positive limit uses default 100", func(t *testing.T) {
			discs := newDiscRepo()
			if _, err := NewGetReportUseCase(newRunRepo(completedRun), discs).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: completedRun.ID().String(), Limit: limit}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if discs.lastLimit != 100 {
				t.Errorf("limit = %d, want 100", discs.lastLimit)
			}
		})
	}

	t.Run("run of another tenant is reported as not found", func(t *testing.T) {
		discs := newDiscRepo()
		_, err := NewGetReportUseCase(newRunRepo(completedRun), discs).Execute(ctx, GetReportQuery{TenantID: tenantB, RunID: completedRun.ID().String()})
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
		if discs.lastRunID != "" {
			t.Error("discrepancies of a foreign run must not be listed")
		}
	})

	t.Run("invalid run id", func(t *testing.T) {
		_, err := NewGetReportUseCase(newRunRepo(), newDiscRepo()).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid run id") {
			t.Fatalf("expected invalid run id error, got %v", err)
		}
	})

	t.Run("unknown run returns ErrRunNotFound", func(t *testing.T) {
		_, err := NewGetReportUseCase(newRunRepo(), newDiscRepo()).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: domain.NewRunID().String()})
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
	})

	t.Run("list error is wrapped", func(t *testing.T) {
		discs := newDiscRepo()
		discs.listErr = errBoom
		_, err := NewGetReportUseCase(newRunRepo(completedRun), discs).Execute(ctx, GetReportQuery{TenantID: tenantA, RunID: completedRun.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "list discrepancies") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestListRunsUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("maps runs for tenant", func(t *testing.T) {
		repo := newRunRepo()
		r1, r2 := pendingRun(tenantA, domain.TypePayment), pendingRun(tenantA, domain.TypeInternal)
		repo.list = []*domain.ReconciliationRun{r1, r2}

		views, err := NewListRunsUseCase(repo).Execute(ctx, tenantA, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 2 || views[0].ID != r1.ID().String() || views[1].Type != "internal_ledger" {
			t.Errorf("unexpected views: %+v", views)
		}
		if repo.lastTenant != domain.TenantID(tenantA) || repo.lastLimit != 5 {
			t.Errorf("List called with tenant=%q limit=%d", repo.lastTenant, repo.lastLimit)
		}
	})

	t.Run("empty tenant lists global runs", func(t *testing.T) {
		repo := newRunRepo()
		if _, err := NewListRunsUseCase(repo).Execute(ctx, "", 5); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastTenant != "" {
			t.Errorf("tenant = %q, want empty", repo.lastTenant)
		}
	})

	t.Run("empty result returns empty non-nil slice", func(t *testing.T) {
		views, err := NewListRunsUseCase(newRunRepo()).Execute(ctx, tenantA, 5)
		if err != nil || views == nil || len(views) != 0 {
			t.Errorf("got %#v, %v", views, err)
		}
	})

	for _, limit := range []int{0, -1} {
		t.Run("non-positive limit uses default 20", func(t *testing.T) {
			repo := newRunRepo()
			if _, err := NewListRunsUseCase(repo).Execute(ctx, tenantA, limit); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.lastLimit != 20 {
				t.Errorf("limit = %d, want 20", repo.lastLimit)
			}
		})
	}

	t.Run("invalid tenant", func(t *testing.T) {
		repo := newRunRepo()
		_, err := NewListRunsUseCase(repo).Execute(ctx, "nope", 5)
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
		if repo.listCalled {
			t.Error("repo should not be called")
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		repo := newRunRepo()
		repo.listErr = errBoom
		_, err := NewListRunsUseCase(repo).Execute(ctx, tenantA, 5)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "list runs") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
