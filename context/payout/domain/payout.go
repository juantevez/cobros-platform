package domain

import (
	"fmt"
	"strings"
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
	failureReason  string // motivo público del fallo (vista, eventos, email, webhook)
	// bankFailureDetail: error crudo del banco. Interno: no se expone.
	bankFailureDetail string
	ledgerEntryKey string // clave de idempotencia del asiento en Ledger
	idempotencyKey string // clave de idempotencia de negocio del desembolso
	initiatedAt    *time.Time
	confirmedAt    *time.Time
	failedAt       *time.Time
	createdAt      time.Time
	updatedAt      time.Time

	events []Event
}

// NewPayout crea un Payout en estado Initiated.
// Emite PayoutInitiatedEvent para que el Ledger registre el movimiento.
// idempotencyKey (no vacía) evita desembolsos duplicados por retries del caller.
func NewPayout(
	id PayoutID,
	tenantID TenantID,
	amount Money,
	bankAccount BankAccountInfo,
	idempotencyKey string,
) (*Payout, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	now := time.Now().UTC()
	ledgerKey := fmt.Sprintf("payout_initiated_%s", id.String())

	p := &Payout{
		id:             id,
		tenantID:       tenantID,
		amount:         amount,
		bankAccount:    bankAccount,
		status:         StatusInitiated,
		ledgerEntryKey: ledgerKey,
		idempotencyKey: strings.TrimSpace(idempotencyKey),
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
	return ReconstitutePayoutWithIdempotency(
		id, tenantID, amount, bankAccount, status,
		bankReference, failureReason, ledgerEntryKey, "",
		initiatedAt, confirmedAt, failedAt, createdAt, updatedAt, "",
	)
}

// ReconstitutePayoutWithIdempotency reconstruye el agregado incluyendo la
// clave de idempotencia persistida.
func ReconstitutePayoutWithIdempotency(
	id PayoutID, tenantID TenantID,
	amount Money, bankAccount BankAccountInfo,
	status PayoutStatus,
	bankReference, failureReason, ledgerEntryKey, idempotencyKey string,
	initiatedAt, confirmedAt, failedAt *time.Time,
	createdAt, updatedAt time.Time,
	bankFailureDetail string,
) *Payout {
	return &Payout{
		bankFailureDetail: bankFailureDetail,
		id: id, tenantID: tenantID,
		amount: amount, bankAccount: bankAccount,
		status: status,
		bankReference: bankReference, failureReason: failureReason,
		ledgerEntryKey: ledgerEntryKey,
		idempotencyKey: idempotencyKey,
		initiatedAt:    initiatedAt, confirmedAt: confirmedAt, failedAt: failedAt,
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
// Permitido desde processing (camino normal) y desde unknown (resolución de
// reconciliación: el banco terminó acreditando una transferencia ambigua).
func (p *Payout) Confirm(bankReference string) error {
	if p.status != StatusProcessing && p.status != StatusUnknown {
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

// Fail registra el fallo definitivo de la transferencia (rechazo conocido del
// banco). Emite PayoutFailedEvent para que el Ledger revierta el asiento.
// Permitido desde processing y desde unknown (resolución de reconciliación:
// se confirma que el banco NO acreditó los fondos). reason es el motivo
// público; detail, el error crudo del banco, que se guarda solo para
// diagnóstico.
func (p *Payout) Fail(reason, detail string) error {
	if p.status != StatusProcessing && p.status != StatusUnknown {
		return fmt.Errorf("%w: cannot fail from %q", ErrInvalidTransition, p.status)
	}
	if reason == "" {
		return ErrFailureReasonRequired
	}

	now := time.Now().UTC()
	p.status = StatusFailed
	p.failureReason = reason
	p.bankFailureDetail = detail
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

// MarkUnknown registra un resultado INDETERMINADO de la transferencia
// (timeout, error de red, respuesta ambigua del banco). A diferencia de Fail,
// NO emite payout.failed.v1: los fondos permanecen en payout_transit hasta que
// la reconciliación resuelva el payout como Confirm o Fail (§10.3).
//
// detail es el error crudo de la transferencia (interno); el motivo público
// es FailureUnconfirmedByBank.
func (p *Payout) MarkUnknown(detail string) error {
	if p.status != StatusProcessing {
		return fmt.Errorf("%w: cannot mark unknown from %q", ErrInvalidTransition, p.status)
	}
	if detail == "" {
		return ErrFailureReasonRequired
	}

	now := time.Now().UTC()
	reason := FailureUnconfirmedByBank
	p.status = StatusUnknown
	p.failureReason = reason
	p.bankFailureDetail = detail
	p.updatedAt = now

	p.record(PayoutUnknownEvent{
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
func (p *Payout) BankFailureDetail() string { return p.bankFailureDetail }
func (p *Payout) LedgerEntryKey() string      { return p.ledgerEntryKey }
func (p *Payout) IdempotencyKey() string      { return p.idempotencyKey }
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

// MatchesRequest verifica si un payout existente corresponde a la misma
// solicitud original para fines de replay idempotente:
//   - amount == 0 en el request significa "saldo completo": no se puede
//     validar el monto, sólo la moneda.
func (p *Payout) MatchesRequest(amount int64, currency string) bool {
	if p.amount.Currency() != strings.ToUpper(strings.TrimSpace(currency)) {
		return false
	}
	if amount == 0 {
		return true
	}
	return p.amount.Amount() == amount
}

func (p *Payout) record(e Event) { p.events = append(p.events, e) }
