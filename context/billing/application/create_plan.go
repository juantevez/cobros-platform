package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// CreatePlanUseCase crea un nuevo plan de tarifas en el catálogo.
// Solo lo puede ejecutar un operador de plataforma (platform_support).
type CreatePlanUseCase struct {
	planRepo  PlanRepository
	txManager TxManager
	publisher EventPublisher
}

func NewCreatePlanUseCase(
	planRepo PlanRepository,
	txManager TxManager,
	publisher EventPublisher,
) *CreatePlanUseCase {
	return &CreatePlanUseCase{planRepo: planRepo, txManager: txManager, publisher: publisher}
}

func (uc *CreatePlanUseCase) Execute(ctx context.Context, cmd CreatePlanCmd) (CreatePlanResult, error) {
	id := domain.NewPlanID()

	plan, err := domain.NewPricingPlan(
		id,
		cmd.Name,
		cmd.Description,
		cmd.BaseRateBps,
		cmd.BaseFixedAmount,
		cmd.MonthlyFee,
		cmd.Currency,
	)
	if err != nil {
		return CreatePlanResult{}, err
	}

	// Agregar overrides por método de pago si los hay.
	for _, mr := range cmd.MethodRates {
		method, err := domain.ParsePaymentMethod(mr.Method)
		if err != nil {
			return CreatePlanResult{}, fmt.Errorf("method rate %q: %w", mr.Method, err)
		}
		if err := plan.AddMethodRate(method, mr.RateBps, mr.FixedAmount); err != nil {
			return CreatePlanResult{}, fmt.Errorf("method rate %q: %w", mr.Method, err)
		}
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.planRepo.Save(ctx, plan); err != nil {
			return fmt.Errorf("save plan: %w", err)
		}
		return uc.publisher.Publish(ctx, plan.PullEvents()...)
	}); err != nil {
		return CreatePlanResult{}, err
	}

	return CreatePlanResult{PlanID: id.String()}, nil
}
