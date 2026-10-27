package application

import (
	"context"
	"errors"
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

// ListPlansPageUseCase lista el catálogo con paginación (limit/offset).
// includeInactive=true expone también los planes desactivados (gestión).
type ListPlansPageUseCase struct{ planRepo PlanRepository }

func NewListPlansPageUseCase(planRepo PlanRepository) *ListPlansPageUseCase {
	return &ListPlansPageUseCase{planRepo: planRepo}
}

func (uc *ListPlansPageUseCase) Execute(ctx context.Context, q ListPlansPageQuery) ([]PlanView, Page, error) {
	page := Page{Limit: q.Limit, Offset: q.Offset}
	page.Normalize()

	plans, page, err := uc.planRepo.ListPage(ctx, PlanListFilter{
		Page:            page,
		IncludeInactive: q.IncludeInactive,
	})
	if err != nil {
		return nil, Page{}, fmt.Errorf("list plans page: %w", err)
	}
	views := make([]PlanView, len(plans))
	for i, p := range plans {
		views[i] = toPlanView(p)
	}
	return views, page, nil
}

// GetTenantPlanUseCase consulta el plan activo de un tenant.
//
// Si el tenant no tiene plan, retorna la tarifa de fallback (Fallback=true)
// en vez de ErrTenantPlanNotFound: es lo que CalculateFee le cobra realmente.
type GetTenantPlanUseCase struct {
	tenantPlanRepo  TenantPlanRepository
	fallbackRateBps int64
}

// fallbackRateBps debe ser el mismo que recibe NewCalculateFeeUseCase
// (<= 0 → DefaultFallbackRateBps).
func NewGetTenantPlanUseCase(tenantPlanRepo TenantPlanRepository, fallbackRateBps int64) *GetTenantPlanUseCase {
	if fallbackRateBps <= 0 {
		fallbackRateBps = DefaultFallbackRateBps
	}
	return &GetTenantPlanUseCase{tenantPlanRepo: tenantPlanRepo, fallbackRateBps: fallbackRateBps}
}

func (uc *GetTenantPlanUseCase) Execute(ctx context.Context, q GetTenantPlanQuery) (TenantPlanView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return TenantPlanView{}, err
	}
	tp, err := uc.tenantPlanRepo.FindActive(ctx, tenantID)
	if errors.Is(err, domain.ErrTenantPlanNotFound) {
		rate := uc.fallbackRateBps
		return TenantPlanView{
			TenantID:        tenantID.String(),
			PlanName:        fallbackPlanName,
			Active:          true,
			Fallback:        true,
			FallbackRateBps: &rate,
		}, nil
	}
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
