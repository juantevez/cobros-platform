package domain

import (
	"fmt"
	"time"
)

// Payout es el agregado raíz del contexto de Desembolsos.
//
// Representa una transferencia de fondos desde la plataforma hacia la
// cuenta bancaria del comercio. El ciclo de vida sigue esta FSM:
//
//	initiated → processing → confirmed   (camino feliz)
//	                       → failed      (banco rechaza)
//
// El asiento contable en el Ledger se crea cuando el payout se inicia y se
// finaliza (confirma o revierte) cuando el banco responde. Esto se hace
// via eventos de dominio, manteniendo el desacoplamiento con el Ledger.
type Payout struct {
	id             PayoutID
	tenantID       TenantID
	amount         Money
	bankAccount    BankAccountInfo
	status         PayoutStatus
	bankReference  string // referencia asignada por el banco/adapter
	failureReason  string
	ledgerEntryKey string // clave de idempotencia del asiento en Ledger
	initiatedAt    *time.Time
	confirmedAt    *time.Time
	failedAt       *time.Time
	createdAt      time.Time
	updatedAt      time.Time

	events []Event
}

// NewPayout crea un Payout en estado Initiated.
// Emite PayoutInitiatedEvent para que el Ledger registre el movimiento.
func NewPayout(
	id PayoutID,
	tenantID TenantID,
	amount Money,
	bankAccount BankAccountInfo,
) (*Payout, error) {
	now := time.Now().UTC()
	ledgerKey := fmt.Sprintf("payout_initiated_%s", id.String())

	p := &Payout{
		id:             id,
		tenantID:       tenantID,
		amount:         amount,
		bankAccount:    bankAccount,
		status:         StatusInitiated,
		ledgerEntryKey: ledgerKey,
		createdAt:      now,
		updatedAt:      now,
	}

	p.record(PayoutInitiatedEvent{
		baseEvent:      newBase(tenantID.String()),
		PayoutID:       id.String(),
		TenantID:       tenantID.String(),
		Amount:         amount.Amount(),
		Currency:       amount.Currency(),
		IdempotencyKey: ledgerKey,
	})

	return p, nil
}

// ReconstitutePayout reconstruye el agregado desde el repositorio.
func ReconstitutePayout(
	id PayoutID, tenantID TenantID,
	amount Money, bankAccount BankAccountInfo,
	status PayoutStatus,
	bankReference, failureReason, ledgerEntryKey string,
	initiatedAt, confirmedAt, failedAt *time.Time,
	createdAt, updatedAt time.Time,
) *Payout {
	return &Payout{
		id: id, tenantID: tenantID,
		amount: amount, bankAccount: bankAccount,
		status: status,
		bankReference: bankReference, failureReason: failureReason,
		ledgerEntryKey: ledgerEntryKey,
		initiatedAt: initiatedAt, confirmedAt: confirmedAt, failedAt: failedAt,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// MarkProcessing indica que la transferencia fue enviada al banco.
func (p *Payout) MarkProcessing() error {
	if p.status != StatusInitiated {
		return fmt.Errorf("%w: cannot mark processing from %q", ErrInvalidTransition, p.status)
	}
	now := time.Now().UTC()
	p.status = StatusProcessing
	p.initiatedAt = &now
	p.updatedAt = now
	return nil
}

// Confirm registra la confirmación del banco.
// Emite PayoutConfirmedEvent para que el Ledger cierre el asiento.
func (p *Payout) Confirm(bankReference string) error {
	if p.status != StatusProcessing {
		return fmt.Errorf("%w: cannot confirm from %q", ErrInvalidTransition, p.status)
	}

	now := time.Now().UTC()
	p.status = StatusConfirmed
	p.bankReference = bankReference
	p.confirmedAt = &now
	p.updatedAt = now

	p.record(PayoutConfirmedEvent{
		baseEvent:     newBase(p.tenantID.String()),
		PayoutID:      p.id.String(),
		TenantID:      p.tenantID.String(),
		Amount:        p.amount.Amount(),
		Currency:      p.amount.Currency(),
		BankReference: bankReference,
	})

	return nil
}

// Fail registra el fallo de la transferencia.
// Emite PayoutFailedEvent para que el Ledger revierta el asiento.
func (p *Payout) Fail(reason string) error {
	if p.status != StatusProcessing {
		return fmt.Errorf("%w: cannot fail from %q", ErrInvalidTransition, p.status)
	}
	if reason == "" {
		return ErrFailureReasonRequired
	}

	now := time.Now().UTC()
	p.status = StatusFailed
	p.failureReason = reason
	p.failedAt = &now
	p.updatedAt = now

	p.record(PayoutFailedEvent{
		baseEvent:     newBase(p.tenantID.String()),
		PayoutID:      p.id.String(),
		TenantID:      p.tenantID.String(),
		Amount:        p.amount.Amount(),
		Currency:      p.amount.Currency(),
		FailureReason: reason,
	})

	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (p *Payout) ID() PayoutID                { return p.id }
func (p *Payout) TenantID() TenantID          { return p.tenantID }
func (p *Payout) Amount() Money               { return p.amount }
func (p *Payout) BankAccount() BankAccountInfo { return p.bankAccount }
func (p *Payout) Status() PayoutStatus        { return p.status }
func (p *Payout) BankReference() string       { return p.bankReference }
func (p *Payout) FailureReason() string       { return p.failureReason }
func (p *Payout) LedgerEntryKey() string      { return p.ledgerEntryKey }
func (p *Payout) InitiatedAt() *time.Time     { return p.initiatedAt }
func (p *Payout) ConfirmedAt() *time.Time     { return p.confirmedAt }
func (p *Payout) FailedAt() *time.Time        { return p.failedAt }
func (p *Payout) CreatedAt() time.Time        { return p.createdAt }
func (p *Payout) UpdatedAt() time.Time        { return p.updatedAt }

func (p *Payout) PullEvents() []Event {
	evs := p.events
	p.events = nil
	return evs
}

func (p *Payout) record(e Event) { p.events = append(p.events, e) }
