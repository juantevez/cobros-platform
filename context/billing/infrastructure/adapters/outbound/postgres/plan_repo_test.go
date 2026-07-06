package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

func TestPlanRepo_SaveAndFindByID(t *testing.T) {
	pool := requireDB(t)
	repo := NewPlanRepository(pool)
	ctx := context.Background()

	p, err := domain.NewPricingPlan(domain.NewPlanID(), "Plan Pro", "premium", 250, 50, 1000, "ARS")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if err := p.AddMethodRate(domain.MethodCard, 300, 60); err != nil {
		t.Fatalf("add method rate: %v", err)
	}
	p.PullEvents()
	cleanupPlan(t, pool, p.ID())

	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID() != p.ID() || got.Name() != "Plan Pro" || got.BaseRateBps() != 250 ||
		got.BaseFixedAmount() != 50 || got.MonthlyFee() != 1000 || got.Currency() != "ARS" {
		t.Errorf("mismatch: %+v", got)
	}
	if !got.Active() {
		t.Error("expected active plan")
	}
	mr, ok := got.MethodRates()[domain.MethodCard]
	if !ok || mr.RateBps != 300 || mr.FixedAmount != 60 {
		t.Errorf("method rate mismatch: %+v", got.MethodRates())
	}
}

func TestPlanRepo_FindByID_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewPlanRepository(pool)
	if _, err := repo.FindByID(context.Background(), domain.NewPlanID()); !errors.Is(err, domain.ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

func TestPlanRepo_Update(t *testing.T) {
	pool := requireDB(t)
	repo := NewPlanRepository(pool)
	ctx := context.Background()

	p := seedPlan(t, pool, "Plan Base", 250, 0)

	// Agregar override y desactivar.
	if err := p.AddMethodRate(domain.MethodWallet, 200, 0); err != nil {
		t.Fatalf("add method rate: %v", err)
	}
	p.Deactivate()
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Active() {
		t.Error("expected plan to be deactivated")
	}
	if _, ok := got.MethodRates()[domain.MethodWallet]; !ok {
		t.Error("expected wallet method rate to be persisted")
	}
}

func TestPlanRepo_UpsertMethodRateOnConflict(t *testing.T) {
	pool := requireDB(t)
	repo := NewPlanRepository(pool)
	ctx := context.Background()

	p := seedPlan(t, pool, "Plan Base", 250, 0)

	// Primer override.
	if err := p.AddMethodRate(domain.MethodCard, 300, 10); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	// Reemplazar el mismo método (ON CONFLICT DO UPDATE).
	if err := p.AddMethodRate(domain.MethodCard, 275, 20); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	got, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	mr := got.MethodRates()[domain.MethodCard]
	if mr.RateBps != 275 || mr.FixedAmount != 20 {
		t.Errorf("expected upserted rate 275/20, got %+v", mr)
	}
}

func TestPlanRepo_ListActive(t *testing.T) {
	pool := requireDB(t)
	repo := NewPlanRepository(pool)
	ctx := context.Background()

	active := seedPlan(t, pool, "AAA Activo Billing Test", 250, 0)
	inactive := seedPlan(t, pool, "ZZZ Inactivo Billing Test", 250, 0)
	inactive.Deactivate()
	if err := repo.Update(ctx, inactive); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	plans, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var foundActive, foundInactive bool
	for _, p := range plans {
		if !p.Active() {
			t.Errorf("ListActive returned inactive plan %s", p.ID())
		}
		if p.ID() == active.ID() {
			foundActive = true
		}
		if p.ID() == inactive.ID() {
			foundInactive = true
		}
	}
	if !foundActive {
		t.Error("expected active plan in ListActive")
	}
	if foundInactive {
		t.Error("did not expect inactive plan in ListActive")
	}
}
