package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

func TestGetPlan_Success(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 50)
	if err := plan.AddMethodRate(domain.MethodCard, 300, 60); err != nil {
		t.Fatalf("add method rate: %v", err)
	}
	uc := NewGetPlanUseCase(newFakePlanRepo(plan))

	view, err := uc.Execute(context.Background(), GetPlanQuery{PlanID: plan.ID().String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.ID != plan.ID().String() || view.Name != "Plan Base" {
		t.Fatalf("unexpected view: %+v", view)
	}
	if view.BaseRatePercent != "2.50%" {
		t.Fatalf("expected 2.50%%, got %q", view.BaseRatePercent)
	}
	if len(view.MethodRates) != 1 || view.MethodRates[0].Method != "card" {
		t.Fatalf("expected 1 card method rate, got %+v", view.MethodRates)
	}
	if view.MethodRates[0].RatePercent != "3.00%" {
		t.Fatalf("expected 3.00%%, got %q", view.MethodRates[0].RatePercent)
	}
}

func TestGetPlan_InvalidID(t *testing.T) {
	uc := NewGetPlanUseCase(newFakePlanRepo())
	_, err := uc.Execute(context.Background(), GetPlanQuery{PlanID: "nope"})
	if err == nil {
		t.Fatal("expected error for invalid plan id")
	}
}

func TestGetPlan_NotFound(t *testing.T) {
	uc := NewGetPlanUseCase(newFakePlanRepo())
	_, err := uc.Execute(context.Background(), GetPlanQuery{PlanID: domain.NewPlanID().String()})
	if !errors.Is(err, domain.ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

func TestListPlansPage_OnlyActiveByDefault(t *testing.T) {
	active := buildPlan(t, "Activo", 250, 0)
	inactive := buildPlan(t, "Inactivo", 250, 0)
	inactive.Deactivate()
	uc := NewListPlansPageUseCase(newFakePlanRepo(active, inactive))

	views, page, err := uc.Execute(context.Background(), ListPlansPageQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 1 || views[0].Name != "Activo" {
		t.Fatalf("expected only the active plan, got %+v", views)
	}
	if page.Limit != 20 || page.Offset != 0 || page.Total != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestListPlansPage_IncludeInactive(t *testing.T) {
	active := buildPlan(t, "Activo", 250, 0)
	inactive := buildPlan(t, "Inactivo", 250, 0)
	inactive.Deactivate()
	uc := NewListPlansPageUseCase(newFakePlanRepo(active, inactive))

	views, page, err := uc.Execute(context.Background(), ListPlansPageQuery{IncludeInactive: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 plans (orden por nombre), got %+v", views)
	}
	if views[0].Name != "Activo" || views[1].Name != "Inactivo" {
		t.Fatalf("expected orden alfabético, got %q, %q", views[0].Name, views[1].Name)
	}
	if page.Total != 2 {
		t.Fatalf("expected total 2, got %d", page.Total)
	}
}

func TestListPlansPage_LimitOffset(t *testing.T) {
	repo := newFakePlanRepo(
		buildPlan(t, "A", 100, 0),
		buildPlan(t, "B", 200, 0),
		buildPlan(t, "C", 300, 0),
	)
	uc := NewListPlansPageUseCase(repo)

	views, page, err := uc.Execute(context.Background(), ListPlansPageQuery{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 2 || views[0].Name != "B" || views[1].Name != "C" {
		t.Fatalf("expected B,C en la página, got %+v", views)
	}
	if page.Limit != 2 || page.Offset != 1 || page.Total != 3 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestListPlansPage_CapsLimit(t *testing.T) {
	uc := NewListPlansPageUseCase(newFakePlanRepo(buildPlan(t, "A", 100, 0)))
	_, page, err := uc.Execute(context.Background(), ListPlansPageQuery{Limit: 5000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Limit != MaxPageSize {
		t.Fatalf("expected limit acotado a %d, got %d", MaxPageSize, page.Limit)
	}
}

func TestListPlansPage_Empty(t *testing.T) {
	uc := NewListPlansPageUseCase(newFakePlanRepo())
	views, page, err := uc.Execute(context.Background(), ListPlansPageQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 0 || page.Total != 0 {
		t.Fatalf("expected 0 plans, got %d (total %d)", len(views), page.Total)
	}
}

func TestListPlansPage_ErrorPropagates(t *testing.T) {
	repo := newFakePlanRepo()
	repo.listErr = errBoom
	uc := NewListPlansPageUseCase(repo)

	_, _, err := uc.Execute(context.Background(), ListPlansPageQuery{})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestGetTenantPlan_Success(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()
	tid, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	tpRepo.active[tid] = buildTenantPlan(t, tid, plan)

	uc := NewGetTenantPlanUseCase(tpRepo)
	view, err := uc.Execute(context.Background(), GetTenantPlanQuery{TenantID: tid.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.TenantID != tid.String() || view.PlanName != "Plan Base" {
		t.Fatalf("unexpected view: %+v", view)
	}
	if !view.Active {
		t.Fatal("expected active tenant plan")
	}
}

func TestGetTenantPlan_InvalidID(t *testing.T) {
	uc := NewGetTenantPlanUseCase(newFakeTenantPlanRepo())
	_, err := uc.Execute(context.Background(), GetTenantPlanQuery{TenantID: "nope"})
	if err == nil {
		t.Fatal("expected error for invalid tenant id")
	}
}

func TestGetTenantPlan_NotFound(t *testing.T) {
	uc := NewGetTenantPlanUseCase(newFakeTenantPlanRepo())
	_, err := uc.Execute(context.Background(), GetTenantPlanQuery{TenantID: validUUID()})
	if !errors.Is(err, domain.ErrTenantPlanNotFound) {
		t.Fatalf("expected ErrTenantPlanNotFound, got %v", err)
	}
}
