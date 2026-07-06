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
		if r.gotStatusFilter != "open" || r.gotLimit != 10 {
			t.Errorf("filter/limit not forwarded: %q / %d", r.gotStatusFilter, r.gotLimit)
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

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewListAlertsUseCase(newAlertRepo())
		if _, err := uc.Execute(context.Background(), ListAlertsQuery{TenantID: "bad"}); err == nil {
			t.Fatal("expected error")
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

	t.Run("returns view for owned alert", func(t *testing.T) {
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		uc := NewGetAlertUseCase(r)
		v, err := uc.Execute(context.Background(), GetAlertQuery{TenantID: tid.String(), AlertID: a.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ID != a.ID().String() {
			t.Errorf("view id = %q", v.ID)
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewGetAlertUseCase(newAlertRepo())
		if _, err := uc.Execute(context.Background(), GetAlertQuery{TenantID: "bad", AlertID: uuid.NewString()}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid alert id rejected", func(t *testing.T) {
		uc := NewGetAlertUseCase(newAlertRepo())
		if _, err := uc.Execute(context.Background(), GetAlertQuery{TenantID: validTenant(), AlertID: "bad"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("not found propagated", func(t *testing.T) {
		uc := NewGetAlertUseCase(newAlertRepo())
		_, err := uc.Execute(context.Background(), GetAlertQuery{TenantID: validTenant(), AlertID: uuid.NewString()})
		if !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})

	t.Run("cross-tenant denied", func(t *testing.T) {
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		uc := NewGetAlertUseCase(r)
		_, err := uc.Execute(context.Background(), GetAlertQuery{TenantID: validTenant(), AlertID: a.ID().String()})
		if !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})
}

func TestAddWatchlistEntryUseCase_Execute(t *testing.T) {
	t.Run("adds normalized entry", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := NewAddWatchlistEntryUseCase(w, newClock())
		cmd := AddWatchlistEntryCmd{FullName: "  Juan   Perez ", ListType: "pep", Country: "AR", Source: "local"}
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.added) != 1 {
			t.Fatalf("expected 1 entry added, got %d", len(w.added))
		}
		if w.gotNormAdd != "juan perez" {
			t.Errorf("normalized name = %q", w.gotNormAdd)
		}
		if w.added[0].ListType != "pep" || w.added[0].FullName != "  Juan   Perez " {
			t.Errorf("entry mismatch: %+v", w.added[0])
		}
		if _, err := uuid.Parse(w.added[0].ID); err != nil {
			t.Errorf("entry ID not a uuid: %q", w.added[0].ID)
		}
	})

	t.Run("add error propagated", func(t *testing.T) {
		w := &fakeWatchlist{addErr: errBoom}
		uc := NewAddWatchlistEntryUseCase(w, newClock())
		if err := uc.Execute(context.Background(), AddWatchlistEntryCmd{FullName: "x"}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
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
		a := domain.ReconstituteAlert(domain.NewAlertID(), tid, domain.AlertSanctionsMatch, domain.RiskHigh,
			domain.StatusConfirmed, "s", 95, nil, "note", testNow, &resolved)
		v := toAlertView(a)
		if v.ResolvedAt == nil {
			t.Fatal("expected non-nil resolvedAt")
		}
		if *v.ResolvedAt != resolved.Format(time.RFC3339) {
			t.Errorf("resolvedAt = %q", *v.ResolvedAt)
		}
		if v.Note != "note" {
			t.Errorf("note = %q", v.Note)
		}
	})
}
