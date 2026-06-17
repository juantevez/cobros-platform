package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// StartReconciliationUseCase crea un nuevo run de reconciliación en estado pending.
// El run queda listo para ser procesado con ProcessReport o ProcessInternal.
type StartReconciliationUseCase struct {
	runRepo TxManager
	repo    RunRepository
}

func NewStartReconciliationUseCase(repo RunRepository) *StartReconciliationUseCase {
	return &StartReconciliationUseCase{repo: repo}
}

func (uc *StartReconciliationUseCase) Execute(ctx context.Context, cmd StartReconciliationCmd) (StartReconciliationResult, error) {
	reconcType, err := domain.ParseReconciliationType(cmd.Type)
	if err != nil {
		return StartReconciliationResult{}, err
	}

	if cmd.PeriodFrom.IsZero() || cmd.PeriodTo.IsZero() {
		return StartReconciliationResult{}, fmt.Errorf("period_from and period_to are required")
	}

	var tenantID domain.TenantID
	if cmd.TenantID != "" {
		tenantID, err = domain.ParseTenantID(cmd.TenantID)
		if err != nil {
			return StartReconciliationResult{}, err
		}
	}

	id := domain.NewRunID()
	run, err := domain.NewReconciliationRun(id, tenantID, reconcType, cmd.PeriodFrom, cmd.PeriodTo)
	if err != nil {
		return StartReconciliationResult{}, err
	}

	if err := uc.repo.Save(ctx, run); err != nil {
		return StartReconciliationResult{}, fmt.Errorf("save run: %w", err)
	}

	return StartReconciliationResult{RunID: id.String()}, nil
}
