package domain

import (
	"time"

	"github.com/google/uuid"
)

type Event interface {
	EventID() string
	EventType() string
	EventTenantID() string
	OccurredAt() time.Time
}

type baseEvent struct {
	id         string
	tenantID   string
	occurredAt time.Time
}

func newBase(tenantID string) baseEvent {
	return baseEvent{id: uuid.NewString(), tenantID: tenantID, occurredAt: time.Now().UTC()}
}

func (e baseEvent) EventID() string       { return e.id }
func (e baseEvent) EventTenantID() string { return e.tenantID }
func (e baseEvent) OccurredAt() time.Time { return e.occurredAt }

// ReconciliationCompletedEvent se emite cuando un run finaliza.
// Útil para notificaciones al operador y auditoría.
type ReconciliationCompletedEvent struct {
	baseEvent
	RunID            string `json:"run_id"`
	TenantID         string `json:"tenant_id"`
	Type             string `json:"type"`
	TotalRecords     int    `json:"total_records"`
	MatchedCount     int    `json:"matched_count"`
	DiscrepancyCount int    `json:"discrepancy_count"`
}

func (e ReconciliationCompletedEvent) EventType() string {
	return "reconciliation.run.completed.v1"
}
