package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

func validTenant() string { return uuid.NewString() }

func TestScreenApplicationUseCase_Execute(t *testing.T) {
	baseCmd := func() ScreenApplicationCmd {
		return ScreenApplicationCmd{
			TenantID:      validTenant(),
			ApplicationID: "app-1",
			LegalName:     "Osama Bin Laden",
		}
	}

	newUC := func(w *fakeWatchlist, r *fakeAlertRepo, p *fakePublisher) *ScreenApplicationUseCase {
		return NewScreenApplicationUseCase(r, w, fakeTx{}, p, newClock())
	}

	t.Run("match raises sanctions alert with best score", func(t *testing.T) {
		w := &fakeWatchlist{matches: []domain.Match{
			matchOf("Osama Bin Laden", "sanctions", "SA", "OFAC", 70),
			matchOf("Osama B. Laden", "sanctions", "SA", "UN", 95), // mejor score
		}}
		r := newAlertRepo()
		p := &fakePublisher{}
		uc := newUC(w, r, p)

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 1 {
			t.Fatalf("expected 1 alert saved, got %d", len(r.saved))
		}
		a := r.saved[0]
		if a.Type() != domain.AlertSanctionsMatch {
			t.Errorf("type = %q, want sanctions_match", a.Type())
		}
		if a.Score() != 95 || a.RiskLevel() != domain.RiskHigh {
			t.Errorf("score/risk mismatch: %d / %q", a.Score(), a.RiskLevel())
		}
		if a.Details()["matched_name"] != "Osama B. Laden" || a.Details()["source"] != "UN" {
			t.Errorf("details mismatch: %+v", a.Details())
		}
		if a.Details()["application_id"] != "app-1" {
			t.Errorf("application_id missing: %+v", a.Details())
		}
		if len(p.published) != 1 {
			t.Errorf("expected 1 published event, got %d", len(p.published))
		}
	})

	t.Run("screening normalizes the legal name", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := newUC(w, newAlertRepo(), &fakePublisher{})
		cmd := baseCmd()
		cmd.LegalName = "  OSAMA   Bin  Laden "
		_ = uc.Execute(context.Background(), cmd)
		if w.gotNormScreen != "osama bin laden" {
			t.Errorf("normalized name = %q", w.gotNormScreen)
		}
	})

	t.Run("no match: no alert", func(t *testing.T) {
		w := &fakeWatchlist{matches: nil}
		r := newAlertRepo()
		uc := newUC(w, r, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 0 {
			t.Error("no alert should be raised without matches")
		}
	})

	t.Run("empty legal name is a no-op", func(t *testing.T) {
		w := &fakeWatchlist{}
		uc := newUC(w, newAlertRepo(), &fakePublisher{})
		cmd := baseCmd()
		cmd.LegalName = ""
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.gotNormScreen != "" {
			t.Error("watchlist should not be queried for empty name")
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := newUC(&fakeWatchlist{}, newAlertRepo(), &fakePublisher{})
		cmd := baseCmd()
		cmd.TenantID = "not-a-uuid"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for invalid tenant")
		}
	})

	t.Run("screen error propagated", func(t *testing.T) {
		w := &fakeWatchlist{screenErr: errBoom}
		uc := newUC(w, newAlertRepo(), &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("duplicate alert is idempotent (no publish)", func(t *testing.T) {
		w := &fakeWatchlist{matches: []domain.Match{matchOf("x", "sanctions", "", "", 95)}}
		r := newAlertRepo()
		r.saveErr = domain.ErrDuplicateAlert
		p := &fakePublisher{}
		uc := newUC(w, r, p)

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("duplicate should be swallowed, got %v", err)
		}
		if len(p.published) != 0 {
			t.Error("no event should be published on duplicate")
		}
	})

	t.Run("save error propagated", func(t *testing.T) {
		w := &fakeWatchlist{matches: []domain.Match{matchOf("x", "sanctions", "", "", 95)}}
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(w, r, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
