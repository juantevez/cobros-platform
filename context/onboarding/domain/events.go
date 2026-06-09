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

// ── Eventos ───────────────────────────────────────────────────────────────────

// ApplicationSubmittedEvent se emite cuando el comercio crea la solicitud inicial.
type ApplicationSubmittedEvent struct {
	baseEvent
	ApplicationID string `json:"application_id"`
	TenantID      string `json:"tenant_id"`
	LegalName     string `json:"legal_name"`
}

func (e ApplicationSubmittedEvent) EventType() string { return "onboarding.application.submitted.v1" }

// ApplicationSentForReviewEvent se emite cuando el comercio envía a revisión.
type ApplicationSentForReviewEvent struct {
	baseEvent
	ApplicationID string `json:"application_id"`
	TenantID      string `json:"tenant_id"`
}

func (e ApplicationSentForReviewEvent) EventType() string {
	return "onboarding.application.sent_for_review.v1"
}

// ApplicationApprovedEvent se emite cuando el operador aprueba el KYC.
//
// CRÍTICO: otros contextos reaccionan a este evento:
//   - Auth: activa el Tenant en modo producción.
//   - Ledger: crea las cuentas contables del comercio.
type ApplicationApprovedEvent struct {
	baseEvent
	ApplicationID    string `json:"application_id"`
	TenantID         string `json:"tenant_id"`
	BusinessCategory string `json:"business_category"`
	// Currency es la moneda principal del comercio (para crear sus cuentas en Ledger).
	Currency string `json:"currency"`
}

func (e ApplicationApprovedEvent) EventType() string { return "onboarding.application.approved.v1" }

// ApplicationRejectedEvent se emite cuando el operador rechaza.
type ApplicationRejectedEvent struct {
	baseEvent
	ApplicationID   string `json:"application_id"`
	TenantID        string `json:"tenant_id"`
	RejectionReason string `json:"rejection_reason"`
}

func (e ApplicationRejectedEvent) EventType() string { return "onboarding.application.rejected.v1" }

// MoreInfoRequestedEvent se emite cuando el operador pide más documentación.
type MoreInfoRequestedEvent struct {
	baseEvent
	ApplicationID string `json:"application_id"`
	TenantID      string `json:"tenant_id"`
	Notes         string `json:"notes"`
}

func (e MoreInfoRequestedEvent) EventType() string { return "onboarding.application.more_info.v1" }
