package fees

import (
	"context"
	"errors"
	"strings"
	"testing"

	billingapp "github.com/juantevez/cobros-platform/context/billing/application"
	billingdomain "github.com/juantevez/cobros-platform/context/billing/domain"
	"github.com/juantevez/cobros-platform/context/payment/domain"
)

const testTenant = domain.TenantID("11111111-1111-1111-1111-111111111111")

func ars(n int64) domain.Money { return domain.ReconstituteMoney(n, "ARS") }

// ── FixedRateCalculator ───────────────────────────────────────────────────────

func TestFixedRateCalculator_Calculate(t *testing.T) {
	cases := []struct {
		name    string
		rateBps int64
		amount  int64
		want    int64
	}{
		{"exact 3%", 300, 10_000, 300},
		{"rounds up fractional cents", 300, 10_001, 301},
		{"tiny amount has minimum fee of 1", 300, 1, 1},
		{"100% rate", 10_000, 777, 777},
		{"zero rate means no fee", 0, 10_000, 0},
		{"negative rate means no fee", -50, 10_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewFixedRateCalculator(tc.rateBps).Calculate(context.Background(), testTenant, ars(tc.amount), domain.MethodCard)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Amount() != tc.want || got.Currency() != "ARS" {
				t.Errorf("fee = %v, want %d ARS", got, tc.want)
			}
		})
	}

	t.Run("keeps payment currency", func(t *testing.T) {
		got, _ := NewFixedRateCalculator(300).Calculate(context.Background(), testTenant, domain.ReconstituteMoney(1000, "USD"), domain.MethodCard)
		if got.Currency() != "USD" {
			t.Errorf("currency = %q, want USD", got.Currency())
		}
	})
}

// ── BillingFeeCalculator ──────────────────────────────────────────────────────

// fakeTenantPlans implementa billing TenantPlanRepository; FindActive devuelve err.
type fakeTenantPlans struct{ err error }

func (f fakeTenantPlans) Save(context.Context, *billingdomain.TenantPlan) error   { return nil }
func (f fakeTenantPlans) Update(context.Context, *billingdomain.TenantPlan) error { return nil }
func (f fakeTenantPlans) FindActive(context.Context, billingdomain.TenantID) (*billingdomain.TenantPlan, error) {
	return nil, f.err
}
func (f fakeTenantPlans) ListByTenant(context.Context, billingdomain.TenantID) ([]*billingdomain.TenantPlan, error) {
	return nil, nil
}

// fakePlans implementa billing PlanRepository; no se usa en los caminos testeados.
type fakePlans struct{}

func (fakePlans) Save(context.Context, *billingdomain.PricingPlan) error   { return nil }
func (fakePlans) Update(context.Context, *billingdomain.PricingPlan) error { return nil }
func (fakePlans) FindByID(context.Context, billingdomain.PlanID) (*billingdomain.PricingPlan, error) {
	return nil, errors.New("not used")
}
func (fakePlans) ListActive(context.Context) ([]*billingdomain.PricingPlan, error) { return nil, nil }

func newBillingCalc(findErr error, fallbackBps int64) *BillingFeeCalculator {
	return NewBillingFeeCalculator(billingapp.NewCalculateFeeUseCase(fakePlans{}, fakeTenantPlans{err: findErr}, fallbackBps))
}

func TestBillingFeeCalculator_Calculate(t *testing.T) {
	t.Run("maps billing result to payment money", func(t *testing.T) {
		// Sin plan asignado, billing aplica el fallback (250 bps).
		calc := newBillingCalc(billingdomain.ErrTenantPlanNotFound, 250)

		got, err := calc.Calculate(context.Background(), testTenant, ars(10_000), domain.MethodCard)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Amount() != 250 || got.Currency() != "ARS" {
			t.Errorf("fee = %v, want 250 ARS", got)
		}
	})

	t.Run("billing error is wrapped", func(t *testing.T) {
		boom := errors.New("db down")
		calc := newBillingCalc(boom, 250)

		_, err := calc.Calculate(context.Background(), testTenant, ars(10_000), domain.MethodCard)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "billing fee calculator") {
			t.Fatalf("expected wrapped error, got %v", err)
		}
	})

	t.Run("invalid inputs rejected by billing are wrapped", func(t *testing.T) {
		calc := newBillingCalc(billingdomain.ErrTenantPlanNotFound, 250)

		_, err := calc.Calculate(context.Background(), domain.TenantID("not-a-uuid"), ars(10_000), domain.MethodCard)
		if err == nil || !strings.Contains(err.Error(), "billing fee calculator") {
			t.Fatalf("expected wrapped error, got %v", err)
		}
	})
}
