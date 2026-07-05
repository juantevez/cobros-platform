package domain

import (
	"time"

	"github.com/google/uuid"
)

// Event es el contrato de los eventos de dominio de Compliance.
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

// AlertRaisedEvent se emite cuando se genera una alerta de compliance.
// Disponible para auditoría, notificaciones y webhooks (sin enforcement automático).
type AlertRaisedEvent struct {
	baseEvent
	AlertID   string `json:"alert_id"`
	TenantID  string `json:"tenant_id"`
	AlertType string `json:"alert_type"`
	RiskLevel string `json:"risk_level"`
	Subject   string `json:"subject"`
	Score     int    `json:"score"`
}

func (e AlertRaisedEvent) EventType() string { return "compliance.alert.raised.v1" }

// AlertResolvedEvent se emite cuando un analista dispone una alerta.
type AlertResolvedEvent struct {
	baseEvent
	AlertID  string `json:"alert_id"`
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"` // "cleared" | "confirmed"
}

func (e AlertResolvedEvent) EventType() string { return "compliance.alert.resolved.v1" }
