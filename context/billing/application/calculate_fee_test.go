package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

func TestCalculateFee_WithTenantPlan(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 50)
	planRepo := newFakePlanRepo(plan)
	tpRepo := newFakeTenantPlanRepo()

	tid, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	tpRepo.active[tid] = buildTenantPlan(t, tid, plan)

	uc := NewCalculateFeeUseCase(planRepo, tpRepo, 300)
	res, err := uc.Execute(context.Background(), CalculateFeeQuery{
		TenantID: tid.String(), Amount: 1_000_000, Currency: "ARS", PaymentMethod: "card",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// rate = ceil(1_000_000 * 250 / 10_000) = 25_000; +50 fixed = 25_050
	if res.FeeAmount != 25_050 {
		t.Fatalf("expected fee 25050, got %d", res.FeeAmount)
	}
	if res.RateBpsApplied != 250 {
		t.Fatalf("expected rate 250, got %d", res.RateBpsApplied)
	}
	if res.PlanName != "Plan Base" {
		t.Fatalf("expected plan name Plan Base, got %q", res.PlanName)
	}
	if res.TenantOverride {
		t.Fatal("expected no tenant override")
	}
}

func TestCalculateFee_TenantOverrideFlag(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()

	tid, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	tp, err := domain.NewTenantPlan(domain.NewTenantPlanID(), tid, plan, 100, -1, timeNowUTC())
	if err != nil {
		t.Fatalf("build tenant plan: %v", err)
	}
	tp.PullEvents()
	tpRepo.active[tid] = tp

	uc := NewCalculateFeeUseCase(newFakePlanRepo(plan), tpRepo, 300)
	res, err := uc.Execute(context.Background(), CalculateFeeQuery{
		TenantID: tid.String(), Amount: 1_000_000, Currency: "ARS", PaymentMethod: "card",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.TenantOverride {
		t.Fatal("expected tenant override flag to be true")
	}
	if res.RateBpsApplied != 100 {
		t.Fatalf("expected overridden rate 100, got %d", res.RateBpsApplied)
	}
}

func TestCalculateFee_FallbackWhenNoPlan(t *testing.T) {
	uc := NewCalculateFeeUseCase(newFakePlanRepo(), newFakeTenantPlanRepo(), 300)

	res, err := uc.Execute(context.Background(), CalculateFeeQuery{
		TenantID: validUUID(), Amount: 1_000_000, Currency: "ARS", PaymentMethod: "card",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// fallback rate 300: ceil(1_000_000 * 300 / 10_000) = 30_000
	if res.FeeAmount != 30_000 {
		t.Fatalf("expected fallback fee 30000, got %d", res.FeeAmount)
	}
	if res.PlanID != "fallback" {
		t.Fatalf("expected fallback plan id, got %q", res.PlanID)
	}
	if res.RateBpsApplied != 300 {
		t.Fatalf("expected fallback rate 300, got %d", res.RateBpsApplied)
	}
}

func TestCalculateFee_FallbackWhenPlanInactive(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()

	tid, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	tpRepo.active[tid] = buildTenantPlan(t, tid, plan)
	plan.Deactivate() // desactivar después de asignar

	uc := NewCalculateFeeUseCase(newFakePlanRepo(plan), tpRepo, 300)
	res, err := uc.Execute(context.Background(), CalculateFeeQuery{
		TenantID: tid.String(), Amount: 1_000_000, Currency: "ARS", PaymentMethod: "card",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PlanID != "fallback" {
		t.Fatalf("expected fallback due to inactive plan, got %q", res.PlanID)
	}
}

func TestCalculateFee_DefaultFallbackRate(t *testing.T) {
	// fallbackRateBps <= 0 debe caer a 300 por defecto.
	uc := NewCalculateFeeUseCase(newFakePlanRepo(), newFakeTenantPlanRepo(), 0)
	if uc.FallbackRateBps != 300 {
		t.Fatalf("expected default fallback 300, got %d", uc.FallbackRateBps)
	}
}

func TestCalculateFee_ValidationErrors(t *testing.T) {
	uc := NewCalculateFeeUseCase(newFakePlanRepo(), newFakeTenantPlanRepo(), 300)

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CalculateFeeQuery{TenantID: "nope", Amount: 100, Currency: "ARS", PaymentMethod: "card"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid payment method", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CalculateFeeQuery{TenantID: validUUID(), Amount: 100, Currency: "ARS", PaymentMethod: "crypto"})
		if !errors.Is(err, domain.ErrInvalidPaymentMethod) {
			t.Fatalf("expected ErrInvalidPaymentMethod, got %v", err)
		}
	})
	t.Run("non-positive amount", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CalculateFeeQuery{TenantID: validUUID(), Amount: 0, Currency: "ARS", PaymentMethod: "card"})
		if err == nil {
			t.Fatal("expected error for non-positive amount")
		}
	})
}

func TestCalculateFee_FindActiveErrorPropagates(t *testing.T) {
	tpRepo := newFakeTenantPlanRepo()
	tpRepo.findActiveErr = errBoom
	uc := NewCalculateFeeUseCase(newFakePlanRepo(), tpRepo, 300)

	_, err := uc.Execute(context.Background(), CalculateFeeQuery{
		TenantID: validUUID(), Amount: 100, Currency: "ARS", PaymentMethod: "card",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
