package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// ResolveDiscrepancyUseCase permite al operador resolver o ignorar una discrepancia.
type ResolveDiscrepancyUseCase struct {
	discrepancyRepo DiscrepancyRepository
}

func NewResolveDiscrepancyUseCase(repo DiscrepancyRepository) *ResolveDiscrepancyUseCase {
	return &ResolveDiscrepancyUseCase{discrepancyRepo: repo}
}

func (uc *ResolveDiscrepancyUseCase) Execute(ctx context.Context, cmd ResolveDiscrepancyCmd) error {
	if cmd.ResolvedBy == "" {
		return fmt.Errorf("resolved_by is required")
	}

	id, err := domain.ParseDiscrepancyID(cmd.DiscrepancyID)
	if err != nil {
		return err
	}

	d, err := uc.discrepancyRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	switch cmd.Action {
	case "resolve":
		if err := d.Resolve(cmd.ResolvedBy, cmd.Notes); err != nil {
			return err
		}
	case "ignore":
		if err := d.Ignore(cmd.ResolvedBy, cmd.Notes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid action %q: must be 'resolve' or 'ignore'", cmd.Action)
	}

	return uc.discrepancyRepo.Update(ctx, d)
}

// ── GetReport ─────────────────────────────────────────────────────────────────

// GetReportUseCase retorna el reporte completo de un run con sus discrepancias.
type GetReportUseCase struct {
	runRepo         RunRepository
	discrepancyRepo DiscrepancyRepository
}

func NewGetReportUseCase(runRepo RunRepository, discrepancyRepo DiscrepancyRepository) *GetReportUseCase {
	return &GetReportUseCase{runRepo: runRepo, discrepancyRepo: discrepancyRepo}
}

func (uc *GetReportUseCase) Execute(ctx context.Context, q GetReportQuery) (ReportView, error) {
	runID, err := domain.ParseRunID(q.RunID)
	if err != nil {
		return ReportView{}, err
	}

	run, err := uc.runRepo.FindByID(ctx, runID)
	if err != nil {
		return ReportView{}, err
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}

	discrepancies, err := uc.discrepancyRepo.ListByRun(ctx, runID, q.StatusFilter, limit)
	if err != nil {
		return ReportView{}, fmt.Errorf("list discrepancies: %w", err)
	}

	return ReportView{
		Run:           toRunView(run),
		Discrepancies: toDiscrepancyViews(discrepancies),
	}, nil
}

// ListRunsUseCase lista los runs de reconciliación.
type ListRunsUseCase struct {
	runRepo RunRepository
}

func NewListRunsUseCase(repo RunRepository) *ListRunsUseCase {
	return &ListRunsUseCase{runRepo: repo}
}

func (uc *ListRunsUseCase) Execute(ctx context.Context, tenantID string, limit int) ([]RunView, error) {
	var tid domain.TenantID
	if tenantID != "" {
		var err error
		tid, err = domain.ParseTenantID(tenantID)
		if err != nil {
			return nil, err
		}
	}
	if limit <= 0 {
		limit = 20
	}

	runs, err := uc.runRepo.List(ctx, tid, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}

	views := make([]RunView, len(runs))
	for i, r := range runs {
		views[i] = toRunView(r)
	}
	return views, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toRunView(r *domain.ReconciliationRun) RunView {
	v := RunView{
		ID:               r.ID().String(),
		TenantID:         r.TenantID().String(),
		Type:             r.Type().String(),
		Status:           r.Status().String(),
		PeriodFrom:       r.PeriodFrom(),
		PeriodTo:         r.PeriodTo(),
		TotalRecords:     r.TotalRecords(),
		MatchedCount:     r.MatchedCount(),
		DiscrepancyCount: r.DiscrepancyCount(),
		ErrorMsg:         r.ErrorMsg(),
		CreatedAt:        r.CreatedAt().Format(time.RFC3339),
	}
	if r.CompletedAt() != nil {
		s := r.CompletedAt().Format(time.RFC3339)
		v.CompletedAt = &s
	}
	return v
}

func toDiscrepancyViews(ds []*domain.Discrepancy) []DiscrepancyView {
	views := make([]DiscrepancyView, len(ds))
	for i, d := range ds {
		views[i] = DiscrepancyView{
			ID:            d.ID().String(),
			RunID:         d.RunID().String(),
			Type:          d.Type().String(),
			RecordID:      d.RecordID(),
			SystemValue:   d.SystemValue(),
			ExternalValue: d.ExternalValue(),
			Status:        d.Status().String(),
			Notes:         d.Notes(),
			ResolvedBy:    d.ResolvedBy(),
			CreatedAt:     d.CreatedAt().Format(time.RFC3339),
		}
	}
	return views
}
