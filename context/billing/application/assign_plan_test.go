package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

func TestAssignPlan_Success(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	planRepo := newFakePlanRepo(plan)
	tpRepo := newFakeTenantPlanRepo()
	pub := &fakePublisher{}
	uc := NewAssignPlanUseCase(planRepo, tpRepo, fakeTx{}, pub)

	tenantID := validUUID()
	res, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID:          tenantID,
		PlanID:            plan.ID().String(),
		CustomRateBps:     -1,
		CustomFixedAmount: -1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TenantPlanID == "" {
		t.Fatal("expected a tenant plan id")
	}
	if res.PlanName != "Plan Base" {
		t.Fatalf("expected plan name Plan Base, got %q", res.PlanName)
	}
	if tpRepo.saved == nil {
		t.Fatal("tenant plan not saved")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.PlanAssignedEvent); !ok {
		t.Fatalf("expected PlanAssignedEvent, got %T", pub.published[0])
	}
}

func TestAssignPlan_DeactivatesPreviousPlan(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	planRepo := newFakePlanRepo(plan)
	tpRepo := newFakeTenantPlanRepo()

	tid, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	previous := buildTenantPlan(t, tid, plan)
	tpRepo.active[tid] = previous

	uc := NewAssignPlanUseCase(planRepo, tpRepo, fakeTx{}, &fakePublisher{})
	_, err = uc.Execute(context.Background(), AssignPlanCmd{
		TenantID:          tid.String(),
		PlanID:            plan.ID().String(),
		CustomRateBps:     -1,
		CustomFixedAmount: -1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tpRepo.updated == nil {
		t.Fatal("expected previous plan to be updated (deactivated)")
	}
	if tpRepo.updated.Active() {
		t.Fatal("previous plan should have been deactivated")
	}
}

func TestAssignPlan_UsesProvidedValidFrom(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), tpRepo, fakeTx{}, &fakePublisher{})

	vf := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID:          validUUID(),
		PlanID:            plan.ID().String(),
		CustomRateBps:     -1,
		CustomFixedAmount: -1,
		ValidFrom:         vf,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tpRepo.saved.ValidFrom().Equal(vf) {
		t.Fatalf("expected validFrom %v, got %v", vf, tpRepo.saved.ValidFrom())
	}
}

func TestAssignPlan_ValidationErrors(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), newFakeTenantPlanRepo(), fakeTx{}, &fakePublisher{})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), AssignPlanCmd{TenantID: "nope", PlanID: plan.ID().String()})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid plan id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), AssignPlanCmd{TenantID: validUUID(), PlanID: "nope"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestAssignPlan_PlanNotFound(t *testing.T) {
	uc := NewAssignPlanUseCase(newFakePlanRepo(), newFakeTenantPlanRepo(), fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID: validUUID(), PlanID: domain.NewPlanID().String(),
		CustomRateBps: -1, CustomFixedAmount: -1,
	})
	if !errors.Is(err, domain.ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

func TestAssignPlan_InactivePlanRejected(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	plan.Deactivate()
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), newFakeTenantPlanRepo(), fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID: validUUID(), PlanID: plan.ID().String(),
		CustomRateBps: -1, CustomFixedAmount: -1,
	})
	if !errors.Is(err, domain.ErrPlanInactive) {
		t.Fatalf("expected ErrPlanInactive, got %v", err)
	}
}

func TestAssignPlan_FindActiveErrorPropagates(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()
	tpRepo.findActiveErr = errBoom
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), tpRepo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID: validUUID(), PlanID: plan.ID().String(),
		CustomRateBps: -1, CustomFixedAmount: -1,
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestAssignPlan_SaveErrorPropagates(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	tpRepo := newFakeTenantPlanRepo()
	tpRepo.saveErr = errBoom
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), tpRepo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID: validUUID(), PlanID: plan.ID().String(),
		CustomRateBps: -1, CustomFixedAmount: -1,
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestAssignPlan_InvalidCustomRateRejected(t *testing.T) {
	plan := buildPlan(t, "Plan Base", 250, 0)
	uc := NewAssignPlanUseCase(newFakePlanRepo(plan), newFakeTenantPlanRepo(), fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), AssignPlanCmd{
		TenantID: validUUID(), PlanID: plan.ID().String(),
		CustomRateBps: 20000, CustomFixedAmount: -1,
	})
	if !errors.Is(err, domain.ErrInvalidRateBps) {
		t.Fatalf("expected ErrInvalidRateBps, got %v", err)
	}
}
