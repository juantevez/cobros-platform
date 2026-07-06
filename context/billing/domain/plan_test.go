package domain

import (
	"errors"
	"testing"
	"time"
)

// newPlan crea un plan base rate 2.5% (250 bps) + fijo 50, en ARS.
func newPlan(t *testing.T) *PricingPlan {
	t.Helper()
	p, err := NewPricingPlan(NewPlanID(), "Standard", "plan estándar", 250, 50, 0, "ARS")
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	p.PullEvents()
	return p
}

func TestNewPricingPlan(t *testing.T) {
	t.Run("valid emits PlanCreated", func(t *testing.T) {
		id := NewPlanID()
		p, err := NewPricingPlan(id, "Standard", "", 250, 50, 1000, "ARS")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.Active() || p.BaseRateBps() != 250 || p.MonthlyFee() != 1000 {
			t.Errorf("fields mismatch: %+v", p)
		}
		if p.Description() != "" || p.BaseFixedAmount() != 50 || p.Currency() != "ARS" {
			t.Errorf("getters mismatch: desc=%q fixed=%d cur=%q", p.Description(), p.BaseFixedAmount(), p.Currency())
		}
		if p.UpdatedAt().IsZero() {
			t.Error("updatedAt should be set")
		}
		events := p.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		created, ok := events[0].(PlanCreatedEvent)
		if !ok {
			t.Fatalf("expected PlanCreatedEvent, got %T", events[0])
		}
		if created.PlanID != id.String() || created.RateBps != 250 {
			t.Errorf("event payload mismatch: %+v", created)
		}
	})

	tests := []struct {
		name                                     string
		planName                                 string
		rateBps, fixedAmount, monthlyFee int64
		currency                                 string
		wantErr                                  error
	}{
		{"empty name", "", 250, 50, 0, "ARS", ErrPlanNameEmpty},
		{"rate below 0", "P", -1, 50, 0, "ARS", ErrInvalidRateBps},
		{"rate above 10000", "P", 10001, 50, 0, "ARS", ErrInvalidRateBps},
		{"negative fixed", "P", 250, -1, 0, "ARS", ErrInvalidFixedAmount},
		{"negative monthly", "P", 250, 50, -1, "ARS", ErrInvalidMonthlyFee},
		{"bad currency", "P", 250, 50, 0, "AR", ErrInvalidCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPricingPlan(NewPlanID(), tt.planName, "", tt.rateBps, tt.fixedAmount, tt.monthlyFee, tt.currency)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCalculateFee_Base(t *testing.T) {
	p := newPlan(t) // 250 bps + 50 fijo

	// amount 10000 × 2.5% = 250 exacto; + 50 fijo = 300.
	fb, err := p.CalculateFee(10000, "ARS", MethodCard)
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if fb.RateBpsApplied != 250 || fb.RateAmount != 250 || fb.FixedAmount != 50 {
		t.Errorf("breakdown mismatch: %+v", fb)
	}
	if fb.TotalFee.Amount() != 300 || fb.TotalFee.Currency() != "ARS" {
		t.Errorf("total fee = %s, want 300 ARS", fb.TotalFee)
	}
	if fb.MethodOverride {
		t.Error("no method override expected")
	}
}

func TestCalculateFee_CeilRounding(t *testing.T) {
	p := newPlan(t) // 250 bps + 50 fijo

	// amount 100 × 2.5% = 2.5 → ceil = 3; + 50 = 53.
	fb, _ := p.CalculateFee(100, "ARS", MethodCard)
	if fb.RateAmount != 3 {
		t.Errorf("rate amount = %d, want 3 (ceil of 2.5)", fb.RateAmount)
	}
	if fb.TotalFee.Amount() != 53 {
		t.Errorf("total = %d, want 53", fb.TotalFee.Amount())
	}

	// Caso extremo: 1 centavo a 1 bps (0.01%) = 0.0001 → ceil = 1.
	p2, _ := NewPricingPlan(NewPlanID(), "P", "", 1, 0, 0, "ARS")
	fb2, _ := p2.CalculateFee(1, "ARS", MethodCard)
	if fb2.RateAmount != 1 {
		t.Errorf("rate amount = %d, want 1 (ceil rounds up)", fb2.RateAmount)
	}
}

func TestCalculateFee_MethodOverride(t *testing.T) {
	p := newPlan(t)
	if err := p.AddMethodRate(MethodCard, 100, 10); err != nil { // 1% + 10 solo para card
		t.Fatalf("add method rate: %v", err)
	}

	// card usa el override: 10000 × 1% = 100; + 10 = 110.
	card, _ := p.CalculateFee(10000, "ARS", MethodCard)
	if !card.MethodOverride || card.RateBpsApplied != 100 || card.TotalFee.Amount() != 110 {
		t.Errorf("card override mismatch: %+v", card)
	}

	// wallet no tiene override → tarifa base (250 + 50).
	wallet, _ := p.CalculateFee(10000, "ARS", MethodWallet)
	if wallet.MethodOverride || wallet.TotalFee.Amount() != 300 {
		t.Errorf("wallet should use base rate: %+v", wallet)
	}
}

func TestCalculateFee_Errors(t *testing.T) {
	p := newPlan(t)

	t.Run("non-positive amount", func(t *testing.T) {
		if _, err := p.CalculateFee(0, "ARS", MethodCard); err == nil {
			t.Fatal("expected error for zero amount")
		}
	})
	t.Run("currency mismatch", func(t *testing.T) {
		if _, err := p.CalculateFee(10000, "USD", MethodCard); err == nil {
			t.Fatal("expected error for currency mismatch")
		}
	})
}

func TestAddMethodRate_Validation(t *testing.T) {
	p := newPlan(t)
	if err := p.AddMethodRate(MethodCard, 10001, 0); !errors.Is(err, ErrInvalidRateBps) {
		t.Errorf("expected ErrInvalidRateBps, got %v", err)
	}
	if err := p.AddMethodRate(MethodCard, 100, -1); !errors.Is(err, ErrInvalidFixedAmount) {
		t.Errorf("expected ErrInvalidFixedAmount, got %v", err)
	}
	if err := p.AddMethodRate(MethodCard, 100, 5); err != nil {
		t.Errorf("valid method rate rejected: %v", err)
	}
	if len(p.MethodRates()) != 1 {
		t.Errorf("expected 1 method rate, got %d", len(p.MethodRates()))
	}
}

func TestPlan_Deactivate(t *testing.T) {
	p := newPlan(t)
	if !p.Active() {
		t.Fatal("new plan should be active")
	}
	p.Deactivate()
	if p.Active() {
		t.Error("plan should be inactive after Deactivate")
	}
}

func TestReconstitutePricingPlan(t *testing.T) {
	id := NewPlanID()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// methodRates nil → debe quedar mapa vacío no-nil.
	p := ReconstitutePricingPlan(id, "P", "d", 250, 50, 1000, nil, "ARS", true, created, created)
	if p.ID() != id || p.MethodRates() == nil {
		t.Errorf("reconstitute mismatch: %+v", p)
	}
	if !p.CreatedAt().Equal(created) {
		t.Error("createdAt not restored")
	}
}
