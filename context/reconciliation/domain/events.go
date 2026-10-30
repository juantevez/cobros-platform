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

func newBase(tenantID string, occurredAt time.Time) baseEvent {
	return baseEvent{id: uuid.NewString(), tenantID: tenantID, occurredAt: occurredAt.UTC()}
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

// ReconciliationFailedEvent se emite cuando un run falla (no se pudieron leer
// los datos o guardar el resultado). Lo consume Audit.
type ReconciliationFailedEvent struct {
	baseEvent
	RunID    string `json:"run_id"`
	TenantID string `json:"tenant_id"`
	Type     string `json:"type"`
	Error    string `json:"error"`
}

func (e ReconciliationFailedEvent) EventType() string {
	return "reconciliation.run.failed.v1"
}

// DiscrepancyResolvedEvent se emite cuando un operador resuelve o ignora una
// discrepancia (Status distingue resolved de ignored). Lo consume Audit.
type DiscrepancyResolvedEvent struct {
	baseEvent
	DiscrepancyID string `json:"discrepancy_id"`
	RunID         string `json:"run_id"`
	TenantID      string `json:"tenant_id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	ResolvedBy    string `json:"resolved_by"`
}

func (e DiscrepancyResolvedEvent) EventType() string {
	return "reconciliation.discrepancy.resolved.v1"
}
