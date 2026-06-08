package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

const (
	defaultLogLimit = 50
	maxLogLimit     = 200
)

// ListLogsUseCase retorna entradas del log de auditoría para visualización.
type ListLogsUseCase struct {
	repo AuditLogRepository
}

func NewListLogsUseCase(repo AuditLogRepository) *ListLogsUseCase {
	return &ListLogsUseCase{repo: repo}
}

func (uc *ListLogsUseCase) Execute(ctx context.Context, q ListLogsQuery) ([]LogEntryView, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if limit > maxLogLimit {
		limit = maxLogLimit
	}

	var (
		entries []*domain.AuditLogEntry
		err     error
	)

	if q.TenantID != "" {
		entries, err = uc.repo.ListByTenant(ctx, q.TenantID, limit)
	} else {
		entries, err = uc.repo.ListRecent(ctx, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}

	views := make([]LogEntryView, len(entries))
	for i, e := range entries {
		views[i] = LogEntryView{
			ID:            e.ID(),
			TenantID:      e.TenantID(),
			Actor:         e.Actor(),
			Action:        e.Action().String(),
			ResourceType:  e.ResourceType().String(),
			ResourceID:    e.ResourceID(),
			Metadata:      e.Metadata(),
			CorrelationID: e.CorrelationID(),
			PrevHash:      e.PrevHashHex(),
			Hash:          e.HashHex(),
			CreatedAt:     e.CreatedAt().Format(time.RFC3339),
		}
	}
	return views, nil
}
