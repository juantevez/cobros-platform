package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// CalculateFeeUseCase calcula la comisión real para un pago dado.
//
// Es el caso de uso central de Billing & Fees. Reemplaza al FixedRateCalculator
// placeholder que usaba Payment Processing en Fase 2.
//
// Lógica de resolución de tarifas (de mayor a menor precedencia):
//  1. TenantPlan con customRateBps o customFixedAmount (tarifa negociada)
//  2. PricingPlan con override por método de pago (ej: card tiene tasa distinta)
//  3. PricingPlan con tarifa base (aplica a todos los métodos)
//  4. Fallback: si el tenant no tiene plan asignado, usa FallbackRateBps
//
// El fallback garantiza que Payment Processing nunca falle por falta de plan.
// En producción, todos los tenants activos deben tener un plan asignado: por
// eso cada uso del fallback se loguea como warning y se marca en el resultado.
//
// Un plan desactivado del catálogo sigue aplicándose a los tenants que ya lo
// tenían asignado (grandfathering): desactivar solo cierra el plan a nuevas
// asignaciones; para cambiar la tarifa de un tenant hay que reasignarlo.
type CalculateFeeUseCase struct {
	planRepo       PlanRepository
	tenantPlanRepo TenantPlanRepository
	// FallbackRateBps se usa cuando el tenant no tiene plan asignado.
	// Valor recomendado: DefaultFallbackRateBps. Configurable al instanciar.
	FallbackRateBps int64
	logger          *slog.Logger
}

// DefaultFallbackRateBps es la tasa (3.00%) que se cobra a un tenant sin plan.
const DefaultFallbackRateBps int64 = 300

const fallbackPlanName = "Fallback (sin plan asignado)"

func NewCalculateFeeUseCase(
	planRepo PlanRepository,
	tenantPlanRepo TenantPlanRepository,
	fallbackRateBps int64,
) *CalculateFeeUseCase {
	if fallbackRateBps <= 0 {
		fallbackRateBps = DefaultFallbackRateBps
	}
	return &CalculateFeeUseCase{
		planRepo:        planRepo,
		tenantPlanRepo:  tenantPlanRepo,
		FallbackRateBps: fallbackRateBps,
		logger:          slog.Default(),
	}
}

func (uc *CalculateFeeUseCase) Execute(ctx context.Context, q CalculateFeeQuery) (CalculateFeeResult, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return CalculateFeeResult{}, err
	}

	method, err := domain.ParsePaymentMethod(q.PaymentMethod)
	if err != nil {
		return CalculateFeeResult{}, err
	}

	if q.Amount <= 0 {
		return CalculateFeeResult{}, fmt.Errorf("amount must be positive")
	}

	// ── Intentar calcular con el plan real del tenant ─────────────────────────

	tenantPlan, err := uc.tenantPlanRepo.FindActive(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, domain.ErrTenantPlanNotFound) {
			return CalculateFeeResult{}, fmt.Errorf("find tenant plan: %w", err)
		}
		// Sin plan asignado → usar fallback.
		uc.logger.WarnContext(ctx, "billing: tenant without pricing plan, applying fallback rate",
			"tenant_id", tenantID.String(),
			"fallback_rate_bps", uc.FallbackRateBps,
		)
		return uc.calculateFallback(q.Amount, q.Currency), nil
	}

	// Un plan inactivo se sigue aplicando (grandfathering, ver doc del tipo).
	plan, err := uc.planRepo.FindByID(ctx, tenantPlan.PlanID())
	if err != nil {
		return CalculateFeeResult{}, fmt.Errorf("find plan %s: %w", tenantPlan.PlanID(), err)
	}

	// ── Calcular con el plan real ─────────────────────────────────────────────

	tenantOverride := tenantPlan.CustomRateBps() != nil || tenantPlan.CustomFixedAmount() != nil

	breakdown, err := tenantPlan.CalculateFee(plan, q.Amount, q.Currency, method)
	if err != nil {
		return CalculateFeeResult{}, fmt.Errorf("calculate fee: %w", err)
	}
	if breakdown.CurrencyMismatch {
		uc.logger.WarnContext(ctx, "billing: payment currency differs from plan currency, fixed amounts omitted",
			"tenant_id", tenantID.String(),
			"plan_id", plan.ID().String(),
			"plan_currency", plan.Currency(),
			"payment_currency", q.Currency,
		)
	}

	return CalculateFeeResult{
		FeeAmount:        breakdown.TotalFee.Amount(),
		Currency:         breakdown.TotalFee.Currency(),
		RateBpsApplied:   breakdown.RateBpsApplied,
		RateAmount:       breakdown.RateAmount,
		FixedAmount:      breakdown.FixedAmount,
		PlanID:           plan.ID().String(),
		PlanName:         plan.Name(),
		MethodOverride:   breakdown.MethodOverride,
		TenantOverride:   tenantOverride,
		CurrencyMismatch: breakdown.CurrencyMismatch,
	}, nil
}

// calculateFallback aplica el rate de fallback cuando el tenant no tiene plan.
func (uc *CalculateFeeUseCase) calculateFallback(amount int64, currency string) CalculateFeeResult {
	feeAmount := (amount*uc.FallbackRateBps + 9999) / 10000
	return CalculateFeeResult{
		FeeAmount:      feeAmount,
		Currency:       currency,
		RateBpsApplied: uc.FallbackRateBps,
		RateAmount:     feeAmount,
		FixedAmount:    0,
		PlanID:         "fallback",
		PlanName:       fallbackPlanName,
		MethodOverride: false,
		TenantOverride: false,
		Fallback:       true,
	}
}
