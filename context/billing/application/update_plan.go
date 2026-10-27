package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// parsedMethodRate es un MethodRateInput ya validado/parsed (uso interno).
type parsedMethodRate struct {
	Method      domain.PaymentMethod
	RateBps     int64
	FixedAmount int64
}

// UpdatePlanUseCase corrige las tarifas de un plan existente sin pasar por SQL.
//
// Semántica de reemplazo: MethodRates reemplaza el conjunto completo de
// overrides por método (los omitidos se eliminan). Los campos puntero de
// UpdatePlanCmd aplican PATCH: nil = no tocar.
type UpdatePlanUseCase struct {
	planRepo  PlanRepository
	txManager TxManager
	publisher EventPublisher
}

func NewUpdatePlanUseCase(
	planRepo PlanRepository,
	txManager TxManager,
	publisher EventPublisher,
) *UpdatePlanUseCase {
	return &UpdatePlanUseCase{planRepo: planRepo, txManager: txManager, publisher: publisher}
}

func (uc *UpdatePlanUseCase) Execute(ctx context.Context, cmd UpdatePlanCmd) (PlanView, error) {
	planID, err := domain.ParsePlanID(cmd.PlanID)
	if err != nil {
		return PlanView{}, err
	}

	plan, err := uc.planRepo.FindByID(ctx, planID)
	if err != nil {
		return PlanView{}, err
	}
	if !plan.Active() {
		return PlanView{}, domain.ErrPlanInactive
	}

	// Validar TODO antes de mutar el agregado (patch atómico o nada).
	if cmd.BaseRateBps != nil && (*cmd.BaseRateBps < 0 || *cmd.BaseRateBps > 10000) {
		return PlanView{}, domain.ErrInvalidRateBps
	}
	if cmd.BaseFixedAmount != nil && *cmd.BaseFixedAmount < 0 {
		return PlanView{}, domain.ErrInvalidFixedAmount
	}
	if cmd.MonthlyFee != nil && *cmd.MonthlyFee < 0 {
		return PlanView{}, domain.ErrInvalidMonthlyFee
	}
	parsedRates := make([]parsedMethodRate, 0, len(cmd.MethodRates))
	if cmd.MethodRates != nil {
		for _, mr := range cmd.MethodRates {
			method, err := domain.ParsePaymentMethod(mr.Method)
			if err != nil {
				return PlanView{}, fmt.Errorf("method rate %q: %w", mr.Method, err)
			}
			if mr.RateBps < 0 || mr.RateBps > 10000 {
				return PlanView{}, fmt.Errorf("method rate %q: %w", mr.Method, domain.ErrInvalidRateBps)
			}
			if mr.FixedAmount < 0 {
				return PlanView{}, fmt.Errorf("method rate %q: %w", mr.Method, domain.ErrInvalidFixedAmount)
			}
			parsedRates = append(parsedRates, parsedMethodRate{Method: method, RateBps: mr.RateBps, FixedAmount: mr.FixedAmount})
		}
	}

	// Aplicar mutaciones del agregado.
	if cmd.BaseRateBps != nil || cmd.BaseFixedAmount != nil {
		baseRate, baseFixed := plan.BaseRateBps(), plan.BaseFixedAmount()
		if cmd.BaseRateBps != nil {
			baseRate = *cmd.BaseRateBps
		}
		if cmd.BaseFixedAmount != nil {
			baseFixed = *cmd.BaseFixedAmount
		}
		if err := plan.UpdateBaseRates(baseRate, baseFixed); err != nil {
			return PlanView{}, err
		}
	}
	if cmd.MonthlyFee != nil {
		if err := plan.SetMonthlyFee(*cmd.MonthlyFee); err != nil {
			return PlanView{}, err
		}
	}
	if cmd.MethodRates != nil {
		// Reemplazo completo: eliminar los actuales y aplicar los nuevos.
		for method := range plan.MethodRates() {
			plan.RemoveMethodRate(method)
		}
		for _, mr := range parsedRates {
			if err := plan.AddMethodRate(mr.Method, mr.RateBps, mr.FixedAmount); err != nil {
				return PlanView{}, fmt.Errorf("method rate %q: %w", mr.Method.String(), err)
			}
		}
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.planRepo.Update(ctx, plan); err != nil {
			return fmt.Errorf("update plan: %w", err)
		}
		plan.RecordUpdated()
		return uc.publisher.Publish(ctx, plan.PullEvents()...)
	}); err != nil {
		// Si la tx falla no se persistió nada; descartar el evento en memoria.
		plan.PullEvents()
		return PlanView{}, err
	}

	return toPlanView(plan), nil
}

// DeactivatePlanUseCase desactiva un plan del catálogo.
//
// No desactiva las asignaciones existentes: CalculateFee trata el plan
// inactivo con el fallback (ver §CalculateFee), y el operador debe
// reasignar a los tenants si corresponde. Idempotente.
type DeactivatePlanUseCase struct {
	planRepo  PlanRepository
	txManager TxManager
	publisher EventPublisher
}

func NewDeactivatePlanUseCase(
	planRepo PlanRepository,
	txManager TxManager,
	publisher EventPublisher,
) *DeactivatePlanUseCase {
	return &DeactivatePlanUseCase{planRepo: planRepo, txManager: txManager, publisher: publisher}
}

func (uc *DeactivatePlanUseCase) Execute(ctx context.Context, cmd DeactivatePlanCmd) (PlanView, error) {
	planID, err := domain.ParsePlanID(cmd.PlanID)
	if err != nil {
		return PlanView{}, err
	}

	plan, err := uc.planRepo.FindByID(ctx, planID)
	if err != nil {
		return PlanView{}, err
	}

	if plan.Active() {
		plan.Deactivate()
		plan.RecordDeactivated()

		err = uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.planRepo.Update(ctx, plan); err != nil {
				return fmt.Errorf("deactivate plan: %w", err)
			}
			return uc.publisher.Publish(ctx, plan.PullEvents()...)
		})
		if err != nil {
			plan.PullEvents() // descartar evento no persistido
			return PlanView{}, err
		}
	}

	return toPlanView(plan), nil
}

// ListTenantPlansUseCase lista (con paginación) el historial de asignaciones
// de un tenant: planes activos e inactivos, ordenados por valid_from DESC.
type ListTenantPlansUseCase struct {
	tenantPlanRepo TenantPlanRepository
}

func NewListTenantPlansUseCase(tenantPlanRepo TenantPlanRepository) *ListTenantPlansUseCase {
	return &ListTenantPlansUseCase{tenantPlanRepo: tenantPlanRepo}
}

func (uc *ListTenantPlansUseCase) Execute(ctx context.Context, q ListTenantPlansQuery) ([]TenantPlanView, Page, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, Page{}, err
	}

	page := Page{Limit: q.Limit, Offset: q.Offset}
	page.Normalize()

	tps, page, err := uc.tenantPlanRepo.ListByTenantPage(ctx, tenantID, page)
	if err != nil {
		return nil, Page{}, fmt.Errorf("list tenant plans: %w", err)
	}

	views := make([]TenantPlanView, len(tps))
	for i, tp := range tps {
		views[i] = toTenantPlanView(tp)
	}
	return views, page, nil
}
