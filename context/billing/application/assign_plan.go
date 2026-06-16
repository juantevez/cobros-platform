package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// AssignPlanUseCase asigna un PricingPlan a un tenant.
//
// Si el tenant ya tiene un plan activo, lo desactiva primero.
// Solo puede haber un plan activo por tenant en un momento dado.
type AssignPlanUseCase struct {
	planRepo       PlanRepository
	tenantPlanRepo TenantPlanRepository
	txManager      TxManager
	publisher      EventPublisher
}

func NewAssignPlanUseCase(
	planRepo PlanRepository,
	tenantPlanRepo TenantPlanRepository,
	txManager TxManager,
	publisher EventPublisher,
) *AssignPlanUseCase {
	return &AssignPlanUseCase{
		planRepo:       planRepo,
		tenantPlanRepo: tenantPlanRepo,
		txManager:      txManager,
		publisher:      publisher,
	}
}

func (uc *AssignPlanUseCase) Execute(ctx context.Context, cmd AssignPlanCmd) (AssignPlanResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return AssignPlanResult{}, err
	}

	planID, err := domain.ParsePlanID(cmd.PlanID)
	if err != nil {
		return AssignPlanResult{}, err
	}

	plan, err := uc.planRepo.FindByID(ctx, planID)
	if err != nil {
		return AssignPlanResult{}, fmt.Errorf("find plan: %w", err)
	}
	if !plan.Active() {
		return AssignPlanResult{}, domain.ErrPlanInactive
	}

	validFrom := cmd.ValidFrom
	if validFrom.IsZero() {
		validFrom = time.Now().UTC()
	}

	// Desactivar el plan activo anterior si existe.
	existing, err := uc.tenantPlanRepo.FindActive(ctx, tenantID)
	if err != nil && !errors.Is(err, domain.ErrTenantPlanNotFound) {
		return AssignPlanResult{}, fmt.Errorf("find active tenant plan: %w", err)
	}

	id := domain.NewTenantPlanID()
	tenantPlan, err := domain.NewTenantPlan(
		id, tenantID, plan,
		cmd.CustomRateBps,
		cmd.CustomFixedAmount,
		validFrom,
	)
	if err != nil {
		return AssignPlanResult{}, err
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		// Desactivar plan anterior si existía.
		if existing != nil {
			existing.Deactivate()
			if err := uc.tenantPlanRepo.Update(ctx, existing); err != nil {
				return fmt.Errorf("deactivate previous plan: %w", err)
			}
		}

		if err := uc.tenantPlanRepo.Save(ctx, tenantPlan); err != nil {
			return fmt.Errorf("save tenant plan: %w", err)
		}

		return uc.publisher.Publish(ctx, tenantPlan.PullEvents()...)
	}); err != nil {
		return AssignPlanResult{}, err
	}

	return AssignPlanResult{
		TenantPlanID: id.String(),
		PlanName:     plan.Name(),
	}, nil
}
