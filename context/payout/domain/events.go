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

// PayoutInitiatedEvent se emite cuando un payout es creado y el Ledger debe
// registrar el movimiento de merchant_balance → payout_transit.
type PayoutInitiatedEvent struct {
	baseEvent
	PayoutID       string `json:"payout_id"`
	TenantID       string `json:"tenant_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"` // para idempotencia del asiento
}

func (e PayoutInitiatedEvent) EventType() string { return "payout.initiated.v1" }

// PayoutConfirmedEvent se emite cuando el banco confirma la transferencia.
// El Ledger cierra el movimiento: payout_transit → payout_sent.
type PayoutConfirmedEvent struct {
	baseEvent
	PayoutID      string `json:"payout_id"`
	TenantID      string `json:"tenant_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	BankReference string `json:"bank_reference"`
}

func (e PayoutConfirmedEvent) EventType() string { return "payout.confirmed.v1" }

// PayoutFailedEvent se emite cuando la transferencia falla.
// El Ledger revierte: payout_transit → merchant_balance (reversa del asiento inicial).
type PayoutFailedEvent struct {
	baseEvent
	PayoutID      string `json:"payout_id"`
	TenantID      string `json:"tenant_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	FailureReason string `json:"failure_reason"`
}

func (e PayoutFailedEvent) EventType() string { return "payout.failed.v1" }
