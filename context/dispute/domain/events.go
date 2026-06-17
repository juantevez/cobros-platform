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

// DisputeOpenedEvent se emite cuando el banco notifica una nueva disputa.
// El Ledger lo consume para crear el hold (merchant_balance → dispute_hold).
type DisputeOpenedEvent struct {
	baseEvent
	DisputeID  string `json:"dispute_id"`
	PaymentID  string `json:"payment_id"`
	TenantID   string `json:"tenant_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Reason     string `json:"reason"`
}

func (e DisputeOpenedEvent) EventType() string { return "dispute.opened.v1" }

// DisputeResolvedEvent se emite cuando el banco cierra la disputa.
// El Ledger lo consume para liberar o debitar los fondos del hold.
type DisputeResolvedEvent struct {
	baseEvent
	DisputeID string `json:"dispute_id"`
	PaymentID string `json:"payment_id"`
	TenantID  string `json:"tenant_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Outcome   string `json:"outcome"` // "won" | "lost" | "accepted" | "expired"
}

func (e DisputeResolvedEvent) EventType() string { return "dispute.resolved.v1" }
