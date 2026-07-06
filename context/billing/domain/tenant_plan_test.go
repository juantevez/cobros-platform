package domain

import (
	"errors"
	"testing"
	"time"
)

func assignPlan(t *testing.T, plan *PricingPlan, customRate, customFixed int64) *TenantPlan {
	t.Helper()
	tp, err := NewTenantPlan(NewTenantPlanID(), NewTenantID_forTest(t), plan, customRate, customFixed, time.Now().UTC())
	if err != nil {
		t.Fatalf("assign plan: %v", err)
	}
	tp.PullEvents()
	return tp
}

func NewTenantID_forTest(t *testing.T) TenantID {
	t.Helper()
	id, err := ParseTenantID(NewPlanID().String()) // reutiliza un uuid válido
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return id
}

func TestNewTenantPlan(t *testing.T) {
	t.Run("success without overrides emits PlanAssigned", func(t *testing.T) {
		plan := newPlan(t)
		id := NewTenantPlanID()
		tid := NewTenantID_forTest(t)
		tp, err := NewTenantPlan(id, tid, plan, -1, -1, time.Now().UTC())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tp.CustomRateBps() != nil || tp.CustomFixedAmount() != nil {
			t.Error("expected no overrides")
		}
		if !tp.Active() || tp.PlanName() != "Standard" {
			t.Errorf("fields mismatch: %+v", tp)
		}
		if tp.TenantID() != tid || tp.CreatedAt().IsZero() {
			t.Errorf("tenant/createdAt getters mismatch: %q %v", tp.TenantID(), tp.CreatedAt())
		}
		events := tp.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		assigned, ok := events[0].(PlanAssignedEvent)
		if !ok {
			t.Fatalf("expected PlanAssignedEvent, got %T", events[0])
		}
		if assigned.TenantID != tid.String() || assigned.PlanID != plan.ID().String() {
			t.Errorf("event payload mismatch: %+v", assigned)
		}
	})

	t.Run("with overrides", func(t *testing.T) {
		tp := assignPlan(t, newPlan(t), 100, 20)
		if tp.CustomRateBps() == nil || *tp.CustomRateBps() != 100 {
			t.Errorf("custom rate not set: %v", tp.CustomRateBps())
		}
		if tp.CustomFixedAmount() == nil || *tp.CustomFixedAmount() != 20 {
			t.Errorf("custom fixed not set: %v", tp.CustomFixedAmount())
		}
	})

	t.Run("inactive plan rejected", func(t *testing.T) {
		plan := newPlan(t)
		plan.Deactivate()
		if _, err := NewTenantPlan(NewTenantPlanID(), NewTenantID_forTest(t), plan, -1, -1, time.Now().UTC()); !errors.Is(err, ErrPlanInactive) {
			t.Fatalf("expected ErrPlanInactive, got %v", err)
		}
	})

	t.Run("custom rate above 10000 rejected", func(t *testing.T) {
		if _, err := NewTenantPlan(NewTenantPlanID(), NewTenantID_forTest(t), newPlan(t), 10001, -1, time.Now().UTC()); !errors.Is(err, ErrInvalidRateBps) {
			t.Fatalf("expected ErrInvalidRateBps, got %v", err)
		}
	})
}

func TestTenantPlan_Deactivate(t *testing.T) {
	tp := assignPlan(t, newPlan(t), -1, -1)
	tp.Deactivate()
	if tp.Active() {
		t.Error("tenant plan should be inactive")
	}
	if tp.ValidUntil() == nil {
		t.Error("validUntil should be set on deactivation")
	}
}

func TestTenantPlan_CalculateFee_NoOverrides(t *testing.T) {
	plan := newPlan(t) // 250 bps + 50
	tp := assignPlan(t, plan, -1, -1)

	fb, err := tp.CalculateFee(plan, 10000, "ARS", MethodCard)
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	// Sin overrides → igual que el plan base: 300.
	if fb.TotalFee.Amount() != 300 || fb.RateBpsApplied != 250 {
		t.Errorf("expected base fee 300, got %+v", fb)
	}
}

func TestTenantPlan_CalculateFee_CustomRate(t *testing.T) {
	plan := newPlan(t) // 250 bps + 50
	tp := assignPlan(t, plan, 100, -1) // override solo la tasa a 1%

	fb, _ := tp.CalculateFee(plan, 10000, "ARS", MethodCard)
	// rate 1% de 10000 = 100; fijo del plan = 50 → 150.
	if fb.RateBpsApplied != 100 || fb.FixedAmount != 50 || fb.TotalFee.Amount() != 150 {
		t.Errorf("custom rate mismatch: %+v", fb)
	}
	if fb.MethodOverride {
		t.Error("tenant override should set MethodOverride=false")
	}
}

func TestTenantPlan_CalculateFee_CustomFixed(t *testing.T) {
	plan := newPlan(t) // 250 bps + 50
	tp := assignPlan(t, plan, -1, 0) // override el fijo a 0

	fb, _ := tp.CalculateFee(plan, 10000, "ARS", MethodCard)
	// tasa del plan (250 → 250) + fijo 0 = 250.
	if fb.FixedAmount != 0 || fb.RateBpsApplied != 250 || fb.TotalFee.Amount() != 250 {
		t.Errorf("custom fixed mismatch: %+v", fb)
	}
}

func TestTenantPlan_CalculateFee_PlanMismatch(t *testing.T) {
	plan := newPlan(t)
	tp := assignPlan(t, plan, -1, -1)
	other := newPlan(t) // otro plan con id distinto

	if _, err := tp.CalculateFee(other, 10000, "ARS", MethodCard); err == nil {
		t.Fatal("expected error for plan mismatch")
	}
}

func TestReconstituteTenantPlan(t *testing.T) {
	id := NewTenantPlanID()
	tid := NewTenantID_forTest(t)
	planID := NewPlanID()
	rate := int64(150)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tp := ReconstituteTenantPlan(id, tid, planID, "Custom", &rate, nil, true, created, nil, created)
	if tp.ID() != id || tp.PlanID() != planID || tp.PlanName() != "Custom" {
		t.Errorf("fields not restored: %+v", tp)
	}
	if tp.CustomRateBps() == nil || *tp.CustomRateBps() != 150 {
		t.Error("custom rate not restored")
	}
	if !tp.ValidFrom().Equal(created) {
		t.Error("validFrom not restored")
	}
}
