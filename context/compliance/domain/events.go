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

// AlertResolvedEvent se emite cuando una alerta llega a su estado final.
// En alertas four-eyes, ProposedBy es el primer analista y ResolvedBy el segundo.
type AlertResolvedEvent struct {
	baseEvent
	AlertID    string `json:"alert_id"`
	TenantID   string `json:"tenant_id"`
	Status     string `json:"status"` // "cleared" | "confirmed"
	ResolvedBy string `json:"resolved_by"`
	ProposedBy string `json:"proposed_by,omitempty"`
}

func (e AlertResolvedEvent) EventType() string { return "compliance.alert.resolved.v1" }

// AlertReviewProposedEvent se emite cuando el primer analista propone una
// disposición sobre una alerta four-eyes (queda pending_second_approval).
type AlertReviewProposedEvent struct {
	baseEvent
	AlertID        string `json:"alert_id"`
	TenantID       string `json:"tenant_id"`
	ProposedStatus string `json:"proposed_status"`
	ProposedBy     string `json:"proposed_by"`
}

func (e AlertReviewProposedEvent) EventType() string { return "compliance.alert.review_proposed.v1" }

// AlertProposalRejectedEvent se emite cuando el segundo analista rechaza la
// propuesta y la alerta vuelve a open.
type AlertProposalRejectedEvent struct {
	baseEvent
	AlertID        string `json:"alert_id"`
	TenantID       string `json:"tenant_id"`
	ProposedStatus string `json:"proposed_status"`
	ProposedBy     string `json:"proposed_by"`
	RejectedBy     string `json:"rejected_by"`
	Note           string `json:"note,omitempty"`
}

func (e AlertProposalRejectedEvent) EventType() string {
	return "compliance.alert.proposal_rejected.v1"
}

// ── Enforcement (compliance.sanction.*) ───────────────────────────────────────
//
// Eventos internos de la plataforma: los consume Auth para poner, levantar o
// convertir en suspensión el hold preventivo del comercio. Nunca deben
// reenviarse al comercio (webhooks/notificaciones): delatarían el motivo
// (tipping-off).

// SanctionMatchedEvent se emite ante un match exacto (score 100) contra una
// lista de sanciones: Auth pone el comercio en hold preventivo.
type SanctionMatchedEvent struct {
	baseEvent
	AlertID     string `json:"alert_id"`
	TenantID    string `json:"tenant_id"`
	MatchedName string `json:"matched_name"`
	ListType    string `json:"list_type"`
	Source      string `json:"source"`
	Score       int    `json:"score"`
}

func (e SanctionMatchedEvent) EventType() string { return "compliance.sanction.matched.v1" }

// SanctionClearedEvent se emite cuando una alerta de sanciones se cierra como
// falso positivo: Auth levanta el hold asociado a la alerta (si lo había).
type SanctionClearedEvent struct {
	baseEvent
	AlertID  string `json:"alert_id"`
	TenantID string `json:"tenant_id"`
}

func (e SanctionClearedEvent) EventType() string { return "compliance.sanction.cleared.v1" }

// SanctionConfirmedEvent se emite cuando una alerta de sanciones se confirma:
// Auth suspende al comercio (con motivo genérico).
type SanctionConfirmedEvent struct {
	baseEvent
	AlertID  string `json:"alert_id"`
	TenantID string `json:"tenant_id"`
}

func (e SanctionConfirmedEvent) EventType() string { return "compliance.sanction.confirmed.v1" }
