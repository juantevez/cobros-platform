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

// PlanCreatedEvent se emite cuando el operador crea un nuevo plan de tarifas.
type PlanCreatedEvent struct {
	baseEvent
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	RateBps  int64  `json:"rate_bps"`
}

func (e PlanCreatedEvent) EventType() string { return "billing.plan.created.v1" }

// PlanAssignedEvent se emite cuando se asigna un plan a un tenant.
type PlanAssignedEvent struct {
	baseEvent
	TenantPlanID string `json:"tenant_plan_id"`
	TenantID     string `json:"tenant_id"`
	PlanID       string `json:"plan_id"`
	PlanName     string `json:"plan_name"`
}

func (e PlanAssignedEvent) EventType() string { return "billing.plan.assigned.v1" }
