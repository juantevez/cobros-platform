package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// GetPlanUseCase consulta un plan por ID.
type GetPlanUseCase struct{ planRepo PlanRepository }

func NewGetPlanUseCase(planRepo PlanRepository) *GetPlanUseCase {
	return &GetPlanUseCase{planRepo: planRepo}
}

func (uc *GetPlanUseCase) Execute(ctx context.Context, q GetPlanQuery) (PlanView, error) {
	planID, err := domain.ParsePlanID(q.PlanID)
	if err != nil {
		return PlanView{}, err
	}
	plan, err := uc.planRepo.FindByID(ctx, planID)
	if err != nil {
		return PlanView{}, err
	}
	return toPlanView(plan), nil
}

// ListPlansUseCase lista todos los planes activos del catálogo.
type ListPlansUseCase struct{ planRepo PlanRepository }

func NewListPlansUseCase(planRepo PlanRepository) *ListPlansUseCase {
	return &ListPlansUseCase{planRepo: planRepo}
}

func (uc *ListPlansUseCase) Execute(ctx context.Context) ([]PlanView, error) {
	plans, err := uc.planRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	views := make([]PlanView, len(plans))
	for i, p := range plans {
		views[i] = toPlanView(p)
	}
	return views, nil
}

// GetTenantPlanUseCase consulta el plan activo de un tenant.
type GetTenantPlanUseCase struct{ tenantPlanRepo TenantPlanRepository }

func NewGetTenantPlanUseCase(tenantPlanRepo TenantPlanRepository) *GetTenantPlanUseCase {
	return &GetTenantPlanUseCase{tenantPlanRepo: tenantPlanRepo}
}

func (uc *GetTenantPlanUseCase) Execute(ctx context.Context, q GetTenantPlanQuery) (TenantPlanView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return TenantPlanView{}, err
	}
	tp, err := uc.tenantPlanRepo.FindActive(ctx, tenantID)
	if err != nil {
		return TenantPlanView{}, err
	}
	return toTenantPlanView(tp), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func toPlanView(p *domain.PricingPlan) PlanView {
	methodRates := make([]MethodRateView, 0, len(p.MethodRates()))
	for method, mr := range p.MethodRates() {
		methodRates = append(methodRates, MethodRateView{
			Method:      method.String(),
			RateBps:     mr.RateBps,
			RatePercent: formatRatePercent(mr.RateBps),
			FixedAmount: mr.FixedAmount,
		})
	}

	return PlanView{
		ID:              p.ID().String(),
		Name:            p.Name(),
		Description:     p.Description(),
		BaseRateBps:     p.BaseRateBps(),
		BaseRatePercent: formatRatePercent(p.BaseRateBps()),
		BaseFixedAmount: p.BaseFixedAmount(),
		MonthlyFee:      p.MonthlyFee(),
		Currency:        p.Currency(),
		Active:          p.Active(),
		MethodRates:     methodRates,
		CreatedAt:       p.CreatedAt().Format(time.RFC3339),
	}
}

func toTenantPlanView(tp *domain.TenantPlan) TenantPlanView {
	v := TenantPlanView{
		ID:                tp.ID().String(),
		TenantID:          tp.TenantID().String(),
		PlanID:            tp.PlanID().String(),
		PlanName:          tp.PlanName(),
		CustomRateBps:     tp.CustomRateBps(),
		CustomFixedAmount: tp.CustomFixedAmount(),
		Active:            tp.Active(),
		ValidFrom:         tp.ValidFrom().Format(time.RFC3339),
	}
	if vu := tp.ValidUntil(); vu != nil {
		s := vu.Format(time.RFC3339)
		v.ValidUntil = &s
	}
	return v
}

// formatRatePercent convierte basis points a un string legible: 250 → "2.50%"
func formatRatePercent(bps int64) string {
	whole := bps / 100
	frac := bps % 100
	return fmt.Sprintf("%d.%02d%%", whole, frac)
}
