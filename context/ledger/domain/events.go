package domain

import (
	"time"

	"github.com/google/uuid"
)

// Event es la interfaz base de eventos de dominio del contexto Ledger.
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

// AccountCreatedEvent se emite cuando se crea una cuenta contable.
type AccountCreatedEvent struct {
	baseEvent
	AccountID   string `json:"account_id"`
	TenantID    string `json:"tenant_id"`
	AccountType string `json:"account_type"`
	Currency    string `json:"currency"`
}

func (e AccountCreatedEvent) EventType() string { return "ledger.account.created.v1" }

// EntryPostedEvent se emite cuando un asiento de doble partida es confirmado.
// Es el evento más importante del sistema: otros contextos reaccionan a él
// para ejecutar payouts, calcular comisiones, etc.
type EntryPostedEvent struct {
	baseEvent
	EntryID        string         `json:"entry_id"`
	TenantID       string         `json:"tenant_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	Description    string         `json:"description"`
	Postings       []PostingEvent `json:"postings"`
}

func (e EntryPostedEvent) EventType() string { return "ledger.entry.posted.v1" }

// PostingEvent es la representación serializable de un posting en el evento.
type PostingEvent struct {
	AccountID string `json:"account_id"`
	Direction string `json:"direction"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// EntryReversedEvent se emite cuando se crea un asiento de reversa.
type EntryReversedEvent struct {
	baseEvent
	ReverseEntryID  string `json:"reverse_entry_id"`
	OriginalEntryID string `json:"original_entry_id"`
	TenantID        string `json:"tenant_id"`
}

func (e EntryReversedEvent) EventType() string { return "ledger.entry.reversed.v1" }
