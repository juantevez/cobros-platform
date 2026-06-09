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

// PaymentCapturedEvent es el evento más importante del contexto Payment.
//
// El Ledger lo consume para crear el asiento contable:
//   in_transit     CREDIT  amount
//   merchant_balance DEBIT (amount - platform_fee)
//   platform_fees  DEBIT  platform_fee
//
// Otros contextos (Webhooks, Notifications) también lo consumen.
type PaymentCapturedEvent struct {
	baseEvent
	PaymentID      string `json:"payment_id"`
	TenantID       string `json:"tenant_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	PlatformFee    int64  `json:"platform_fee"`
	PSPFee         int64  `json:"psp_fee"`
	PaymentMethod  string `json:"payment_method"`
	PSPReference   string `json:"psp_reference"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (e PaymentCapturedEvent) EventType() string { return "payment.captured.v1" }

// PaymentFailedEvent se emite cuando el pago es rechazado por el PSP o el riesgo.
type PaymentFailedEvent struct {
	baseEvent
	PaymentID     string `json:"payment_id"`
	TenantID      string `json:"tenant_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	FailureReason string `json:"failure_reason"`
	PaymentMethod string `json:"payment_method"`
}

func (e PaymentFailedEvent) EventType() string { return "payment.failed.v1" }

// PaymentRefundedEvent se emite cuando se completa un reembolso.
type PaymentRefundedEvent struct {
	baseEvent
	PaymentID    string `json:"payment_id"`
	TenantID     string `json:"tenant_id"`
	RefundAmount int64  `json:"refund_amount"`
	Currency     string `json:"currency"`
	PSPReference string `json:"psp_reference"`
}

func (e PaymentRefundedEvent) EventType() string { return "payment.refunded.v1" }
