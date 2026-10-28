package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

func TestListAlertsUseCase_Execute(t *testing.T) {
	tid, _ := domain.ParseTenantID(validTenant())

	t.Run("maps alerts to views", func(t *testing.T) {
		r := newAlertRepo()
		a := domain.NewAlert(domain.NewAlertID(), tid, domain.AlertSanctionsMatch, domain.RiskHigh, "s", 95, nil, testNow)
		a.PullEvents()
		r.listed = []*domain.Alert{a}
		uc := NewListAlertsUseCase(r)

		views, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: tid.String(), StatusFilter: "open", Limit: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 1 || views[0].ID != a.ID().String() || views[0].Status != "open" {
			t.Errorf("unexpected views: %+v", views)
		}
		if r.gotStatusFilter != "open" || r.gotLimit != 10 || r.gotTenantFilter != tid.String() {
			t.Errorf("filter/limit not forwarded: %q / %q / %d", r.gotStatusFilter, r.gotTenantFilter, r.gotLimit)
		}
	})

	t.Run("without tenant lists the whole platform", func(t *testing.T) {
		r := newAlertRepo()
		uc := NewListAlertsUseCase(r)
		if _, err := uc.Execute(context.Background(), ListAlertsQuery{StatusFilter: "pending_second_approval"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.gotTenantFilter != "" || r.gotStatusFilter != "pending_second_approval" {
			t.Errorf("filter = %q / %q", r.gotTenantFilter, r.gotStatusFilter)
		}
	})

	t.Run("default limit when out of range", func(t *testing.T) {
		r := newAlertRepo()
		uc := NewListAlertsUseCase(r)
		for _, lim := range []int{0, -3, 500} {
			r.gotLimit = 0
			if _, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: tid.String(), Limit: lim}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.gotLimit != 50 {
				t.Errorf("limit %d not clamped to 50, got %d", lim, r.gotLimit)
			}
		}
	})

	t.Run("invalid status filter rejected before hitting the repo", func(t *testing.T) {
		r := newAlertRepo()
		uc := NewListAlertsUseCase(r)
		_, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: tid.String(), StatusFilter: "bogus"})
		if !errors.Is(err, domain.ErrInvalidStatusFilter) {
			t.Fatalf("expected ErrInvalidStatusFilter, got %v", err)
		}
		if r.gotLimit != 0 {
			t.Error("repo should not be called")
		}
	})

	t.Run("invalid tenant filter is a typed error", func(t *testing.T) {
		uc := NewListAlertsUseCase(newAlertRepo())
		if _, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: "nope"}); !errors.Is(err, domain.ErrInvalidTenantID) {
			t.Fatalf("expected ErrInvalidTenantID, got %v", err)
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.listErr = errBoom
		uc := NewListAlertsUseCase(r)
		if _, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: tid.String()}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestGetAlertUseCase_Execute(t *testing.T) {
	tid, _ := domain.ParseTenantID(validTenant())

	t.Run("returns view of any tenant's alert (platform review)", func(t *testing.T) {
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		uc := NewGetAlertUseCase(r)
		v, err := uc.Execute(context.Background(), GetAlertQuery{AlertID: a.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ID != a.ID().String() || v.TenantID != tid.String() || !v.RequiresSecondApproval {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("invalid alert id rejected", func(t *testing.T) {
		uc := NewGetAlertUseCase(newAlertRepo())
		if _, err := uc.Execute(context.Background(), GetAlertQuery{AlertID: "bad"}); !errors.Is(err, domain.ErrInvalidAlertID) {
			t.Fatalf("expected ErrInvalidAlertID, got %v", err)
		}
	})

	t.Run("not found propagated", func(t *testing.T) {
		uc := NewGetAlertUseCase(newAlertRepo())
		_, err := uc.Execute(context.Background(), GetAlertQuery{AlertID: uuid.NewString()})
		if !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})
}

func TestAddWatchlistEntryUseCase_Execute(t *testing.T) {
	t.Run("adds normalized entry", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := NewAddWatchlistEntryUseCase(w, newClock())
		cmd := AddWatchlistEntryCmd{FullName: "  Juan   Pérez ", ListType: "pep", Country: "ar", Source: "local"}
		view, err := uc.Execute(context.Background(), cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.added) != 1 {
			t.Fatalf("expected 1 entry added, got %d", len(w.added))
		}
		if w.gotNormAdd != "juan perez" {
			t.Errorf("normalized name = %q", w.gotNormAdd)
		}
		if w.added[0].ListType != "pep" || w.added[0].FullName != "Juan   Pérez" || w.added[0].Country != "AR" {
			t.Errorf("entry mismatch: %+v", w.added[0])
		}
		if _, err := uuid.Parse(w.added[0].ID); err != nil {
			t.Errorf("entry ID not a uuid: %q", w.added[0].ID)
		}
		if view.ID != w.added[0].ID || view.FullName != "Juan   Pérez" {
			t.Errorf("view mismatch: %+v", view)
		}
	})

	t.Run("invalid list type rejected", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := NewAddWatchlistEntryUseCase(w, newClock())
		_, err := uc.Execute(context.Background(), AddWatchlistEntryCmd{FullName: "x", ListType: "other"})
		if !errors.Is(err, domain.ErrInvalidListType) {
			t.Fatalf("expected ErrInvalidListType, got %v", err)
		}
		if len(w.added) != 0 {
			t.Error("nothing should be persisted")
		}
	})

	t.Run("add error propagated", func(t *testing.T) {
		w := &fakeWatchlist{addErr: errBoom}
		uc := NewAddWatchlistEntryUseCase(w, newClock())
		if _, err := uc.Execute(context.Background(), AddWatchlistEntryCmd{FullName: "x", ListType: "pep"}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestRemoveWatchlistEntryUseCase_Execute(t *testing.T) {
	t.Run("removes entry", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := NewRemoveWatchlistEntryUseCase(w, newClock())
		id := uuid.NewString()
		if err := uc.Execute(context.Background(), RemoveWatchlistEntryCmd{EntryID: id}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.removed) != 1 || w.removed[0] != id {
			t.Errorf("removed = %v", w.removed)
		}
	})

	t.Run("non-uuid id is not found", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := NewRemoveWatchlistEntryUseCase(w, newClock())
		if err := uc.Execute(context.Background(), RemoveWatchlistEntryCmd{EntryID: "bad"}); !errors.Is(err, domain.ErrWatchlistEntryNotFound) {
			t.Fatalf("expected ErrWatchlistEntryNotFound, got %v", err)
		}
		if len(w.removed) != 0 {
			t.Error("repo should not be called")
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		w := &fakeWatchlist{removeErr: domain.ErrWatchlistEntryNotFound}
		uc := NewRemoveWatchlistEntryUseCase(w, newClock())
		if err := uc.Execute(context.Background(), RemoveWatchlistEntryCmd{EntryID: uuid.NewString()}); !errors.Is(err, domain.ErrWatchlistEntryNotFound) {
			t.Fatalf("expected ErrWatchlistEntryNotFound, got %v", err)
		}
	})
}

func TestListWatchlistUseCase_Execute(t *testing.T) {
	t.Run("maps entries and clamps limit", func(t *testing.T) {
		w := &fakeWatchlist{entries: []domain.WatchlistEntry{
			{ID: "1", FullName: "A", ListType: "sanctions", Country: "AR", Source: "OFAC"},
		}}
		uc := NewListWatchlistUseCase(w)
		for _, lim := range []int{0, -1, 1000} {
			views, err := uc.Execute(context.Background(), lim)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(views) != 1 || views[0].FullName != "A" || views[0].Source != "OFAC" {
				t.Errorf("unexpected views: %+v", views)
			}
		}
	})

	t.Run("list error propagated", func(t *testing.T) {
		w := &fakeWatchlist{listErr: errBoom}
		uc := NewListWatchlistUseCase(w)
		if _, err := uc.Execute(context.Background(), 10); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestToAlertView(t *testing.T) {
	tid, _ := domain.ParseTenantID(validTenant())
	resolved := testNow.Add(time.Hour)

	t.Run("open alert has nil resolvedAt", func(t *testing.T) {
		a := domain.NewAlert(domain.NewAlertID(), tid, domain.AlertSanctionsMatch, domain.RiskHigh, "s", 95,
			map[string]string{"k": "v"}, testNow)
		v := toAlertView(a)
		if v.ResolvedAt != nil {
			t.Errorf("expected nil resolvedAt, got %v", *v.ResolvedAt)
		}
		if v.Details["k"] != "v" || v.CreatedAt == "" {
			t.Errorf("view mapping mismatch: %+v", v)
		}
	})

	t.Run("resolved alert formats resolvedAt", func(t *testing.T) {
		a := domain.ReconstituteAlert(domain.AlertSnapshot{
			ID: domain.NewAlertID(), TenantID: tid, Type: domain.AlertSanctionsMatch, RiskLevel: domain.RiskHigh,
			Status: domain.StatusConfirmed, Subject: "s", Score: 95, Note: "note", ResolvedBy: "user-1",
			CreatedAt: testNow, ResolvedAt: &resolved,
			ProposedStatus: domain.StatusConfirmed, ProposedBy: "user-0", ProposedAt: &resolved, ApprovalNote: "ok",
		})
		v := toAlertView(a)
		if v.ResolvedAt == nil {
			t.Fatal("expected non-nil resolvedAt")
		}
		if *v.ResolvedAt != resolved.Format(time.RFC3339) {
			t.Errorf("resolvedAt = %q", *v.ResolvedAt)
		}
		if v.Note != "note" || v.ResolvedBy != "user-1" {
			t.Errorf("note/resolvedBy = %q / %q", v.Note, v.ResolvedBy)
		}
		if v.ProposedStatus != "confirmed" || v.ProposedBy != "user-0" || v.ProposedAt == nil || v.ApprovalNote != "ok" {
			t.Errorf("four-eyes fields not mapped: %+v", v)
		}
	})
}
