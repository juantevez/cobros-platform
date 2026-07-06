package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// buildTenantPlan asigna un plan a un tenant, con overrides opcionales (-1 = sin override).
func buildTenantPlan(t *testing.T, tenantID domain.TenantID, plan *domain.PricingPlan, rate, fixed int64) *domain.TenantPlan {
	t.Helper()
	tp, err := domain.NewTenantPlan(domain.NewTenantPlanID(), tenantID, plan, rate, fixed, time.Now().UTC())
	if err != nil {
		t.Fatalf("build tenant plan: %v", err)
	}
	tp.PullEvents()
	return tp
}

func TestTenantPlanRepo_SaveAndFindActive(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	ctx := context.Background()

	plan := seedPlan(t, pool, "Plan Base", 250, 0)
	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)

	tp := buildTenantPlan(t, tenantID, plan, 100, 25)
	if err := repo.Save(ctx, tp); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindActive(ctx, tenantID)
	if err != nil {
		t.Fatalf("find active: %v", err)
	}
	if got.ID() != tp.ID() || got.PlanID() != plan.ID() || got.PlanName() != "Plan Base" {
		t.Errorf("mismatch: %+v", got)
	}
	if got.CustomRateBps() == nil || *got.CustomRateBps() != 100 {
		t.Errorf("custom rate mismatch: %v", got.CustomRateBps())
	}
	if got.CustomFixedAmount() == nil || *got.CustomFixedAmount() != 25 {
		t.Errorf("custom fixed mismatch: %v", got.CustomFixedAmount())
	}
	if !got.Active() {
		t.Error("expected active tenant plan")
	}
}

func TestTenantPlanRepo_FindActive_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	if _, err := repo.FindActive(context.Background(), testTenantID(t)); !errors.Is(err, domain.ErrTenantPlanNotFound) {
		t.Fatalf("expected ErrTenantPlanNotFound, got %v", err)
	}
}

func TestTenantPlanRepo_SaveWithoutOverrides(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	ctx := context.Background()

	plan := seedPlan(t, pool, "Plan Base", 250, 0)
	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)

	tp := buildTenantPlan(t, tenantID, plan, -1, -1)
	if err := repo.Save(ctx, tp); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindActive(ctx, tenantID)
	if err != nil {
		t.Fatalf("find active: %v", err)
	}
	if got.CustomRateBps() != nil || got.CustomFixedAmount() != nil {
		t.Errorf("expected nil overrides, got rate=%v fixed=%v", got.CustomRateBps(), got.CustomFixedAmount())
	}
}

func TestTenantPlanRepo_Update_Deactivate(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	ctx := context.Background()

	plan := seedPlan(t, pool, "Plan Base", 250, 0)
	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)

	tp := buildTenantPlan(t, tenantID, plan, -1, -1)
	if err := repo.Save(ctx, tp); err != nil {
		t.Fatalf("save: %v", err)
	}

	tp.Deactivate()
	if err := repo.Update(ctx, tp); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Ya no debe haber plan activo.
	if _, err := repo.FindActive(ctx, tenantID); !errors.Is(err, domain.ErrTenantPlanNotFound) {
		t.Fatalf("expected no active plan after deactivate, got %v", err)
	}

	// Pero sigue existiendo en el histórico.
	all, err := repo.ListByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 historical plan, got %d", len(all))
	}
	if all[0].Active() || all[0].ValidUntil() == nil {
		t.Error("expected deactivated plan with valid_until set")
	}
}

func TestTenantPlanRepo_ListByTenant_OrderedByValidFromDesc(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	ctx := context.Background()

	plan := seedPlan(t, pool, "Plan Base", 250, 0)
	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)

	// Plan viejo (desactivado) y plan nuevo (activo).
	old, err := domain.NewTenantPlan(domain.NewTenantPlanID(), tenantID, plan, -1, -1,
		time.Now().UTC().Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("build old: %v", err)
	}
	old.PullEvents()
	old.Deactivate()
	if err := repo.Save(ctx, old); err != nil {
		t.Fatalf("save old: %v", err)
	}

	newer := buildTenantPlan(t, tenantID, plan, -1, -1)
	if err := repo.Save(ctx, newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}

	all, err := repo.ListByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(all))
	}
	// Orden por valid_from DESC: el más reciente primero.
	if all[0].ID() != newer.ID() {
		t.Errorf("expected newest plan first, got %s", all[0].ID())
	}
}

func TestTenantPlanRepo_UniqueActivePerTenant(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantPlanRepository(pool)
	ctx := context.Background()

	plan := seedPlan(t, pool, "Plan Base", 250, 0)
	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)

	first := buildTenantPlan(t, tenantID, plan, -1, -1)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("save first: %v", err)
	}

	// El índice único parcial (tenant_id WHERE active) rechaza un segundo activo.
	second := buildTenantPlan(t, tenantID, plan, -1, -1)
	if err := repo.Save(ctx, second); err == nil {
		t.Fatal("expected unique-violation for second active plan on same tenant")
	}
}
