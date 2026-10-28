package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// ── ListAlerts ────────────────────────────────────────────────────────────────

type ListAlertsUseCase struct{ repo AlertRepository }

func NewListAlertsUseCase(repo AlertRepository) *ListAlertsUseCase {
	return &ListAlertsUseCase{repo: repo}
}

func (uc *ListAlertsUseCase) Execute(ctx context.Context, q ListAlertsQuery) ([]AlertView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, err
	}
	status, err := domain.ParseStatusFilter(q.StatusFilter)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	alerts, err := uc.repo.ListByTenant(ctx, tenantID, status, limit)
	if err != nil {
		return nil, err
	}
	views := make([]AlertView, len(alerts))
	for i, a := range alerts {
		views[i] = toAlertView(a)
	}
	return views, nil
}

// ── GetAlert ──────────────────────────────────────────────────────────────────

type GetAlertUseCase struct{ repo AlertRepository }

func NewGetAlertUseCase(repo AlertRepository) *GetAlertUseCase {
	return &GetAlertUseCase{repo: repo}
}

func (uc *GetAlertUseCase) Execute(ctx context.Context, q GetAlertQuery) (AlertView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return AlertView{}, err
	}
	alertID, err := domain.ParseAlertID(q.AlertID)
	if err != nil {
		return AlertView{}, err
	}
	alert, err := uc.repo.FindByID(ctx, alertID)
	if err != nil {
		return AlertView{}, err
	}
	if alert.TenantID() != tenantID {
		return AlertView{}, domain.ErrAlertNotFound
	}
	return toAlertView(alert), nil
}

// ── Watchlist management ──────────────────────────────────────────────────────

type AddWatchlistEntryUseCase struct {
	watchlist WatchlistRepository
	clock     Clock
}

func NewAddWatchlistEntryUseCase(watchlist WatchlistRepository, clock Clock) *AddWatchlistEntryUseCase {
	return &AddWatchlistEntryUseCase{watchlist: watchlist, clock: clock}
}

func (uc *AddWatchlistEntryUseCase) Execute(ctx context.Context, cmd AddWatchlistEntryCmd) (WatchlistEntryView, error) {
	entry, err := domain.NewWatchlistEntry(uuid.NewString(), cmd.FullName, cmd.ListType, cmd.Country, cmd.Source)
	if err != nil {
		return WatchlistEntryView{}, err
	}
	if err := uc.watchlist.Add(ctx, entry, domain.NormalizeName(entry.FullName), uc.clock.Now()); err != nil {
		return WatchlistEntryView{}, err
	}
	return toWatchlistEntryView(entry), nil
}

type RemoveWatchlistEntryUseCase struct {
	watchlist WatchlistRepository
	clock     Clock
}

func NewRemoveWatchlistEntryUseCase(watchlist WatchlistRepository, clock Clock) *RemoveWatchlistEntryUseCase {
	return &RemoveWatchlistEntryUseCase{watchlist: watchlist, clock: clock}
}

// Execute da de baja la entrada (soft delete): deja de participar del
// screening pero se conserva para trazabilidad de alertas pasadas.
func (uc *RemoveWatchlistEntryUseCase) Execute(ctx context.Context, cmd RemoveWatchlistEntryCmd) error {
	if _, err := uuid.Parse(cmd.EntryID); err != nil {
		return domain.ErrWatchlistEntryNotFound
	}
	return uc.watchlist.Remove(ctx, cmd.EntryID, uc.clock.Now())
}

type ListWatchlistUseCase struct{ watchlist WatchlistRepository }

func NewListWatchlistUseCase(watchlist WatchlistRepository) *ListWatchlistUseCase {
	return &ListWatchlistUseCase{watchlist: watchlist}
}

func (uc *ListWatchlistUseCase) Execute(ctx context.Context, limit int) ([]WatchlistEntryView, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	entries, err := uc.watchlist.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	views := make([]WatchlistEntryView, len(entries))
	for i, e := range entries {
		views[i] = toWatchlistEntryView(e)
	}
	return views, nil
}

// ── Mappers ───────────────────────────────────────────────────────────────────

func toWatchlistEntryView(e domain.WatchlistEntry) WatchlistEntryView {
	return WatchlistEntryView{
		ID: e.ID, FullName: e.FullName, ListType: e.ListType,
		Country: e.Country, Source: e.Source,
	}
}

func toAlertView(a *domain.Alert) AlertView {
	var resolvedAt *string
	if a.ResolvedAt() != nil {
		s := a.ResolvedAt().Format(time.RFC3339)
		resolvedAt = &s
	}
	return AlertView{
		ID:         a.ID().String(),
		TenantID:   a.TenantID().String(),
		AlertType:  a.Type().String(),
		RiskLevel:  a.RiskLevel().String(),
		Status:     a.Status().String(),
		Subject:    a.Subject(),
		Score:      a.Score(),
		Details:    a.Details(),
		Note:       a.Note(),
		ResolvedBy: a.ResolvedBy(),
		CreatedAt:  a.CreatedAt().Format(time.RFC3339),
		ResolvedAt: resolvedAt,
	}
}
