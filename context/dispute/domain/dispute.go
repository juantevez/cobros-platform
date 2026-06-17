package domain

import (
	"fmt"
	"time"
)

// Dispute es el agregado raíz del contexto Disputes & Chargebacks.
//
// Representa una impugnación de pago iniciada por el pagador ante su banco.
// Cuando el banco notifica una disputa, la plataforma congela los fondos
// del comercio y le da un plazo para contestar con evidencia.
//
// FSM:
//
//	open → under_review   (comercio contesta con evidencia)
//	open → accepted       (comercio acepta la disputa)
//	open → expired        (venció el plazo sin respuesta)
//	under_review → won    (banco falla a favor del comercio)
//	under_review → lost   (banco falla a favor del pagador)
//
// Impacto contable (via eventos al Ledger):
//
//	opened:   merchant_balance CREDIT → dispute_hold DEBIT   (congelar fondos)
//	won:      dispute_hold CREDIT → merchant_balance DEBIT   (liberar fondos)
//	lost:     dispute_hold CREDIT → (fondos devueltos al banco, salen del sistema)
//	accepted: igual que lost
type Dispute struct {
	id           DisputeID
	tenantID     TenantID
	paymentID    string   // referencia al pago disputado
	pspReference string   // referencia del PSP para la disputa
	amount       int64    // monto disputado en centavos
	currency     string
	reason       DisputeReason
	status       DisputeStatus
	evidence     []Evidence
	responseNote string   // nota del comercio al contestar
	resolvedNote string   // nota del operador al cerrar
	deadline     time.Time  // fecha límite para contestar
	openedAt     time.Time
	respondedAt  *time.Time
	resolvedAt   *time.Time

	events []Event
}

// NewDispute crea una disputa en estado Open.
// Se llama cuando el banco/PSP notifica al sistema sobre una nueva disputa.
func NewDispute(
	id DisputeID,
	tenantID TenantID,
	paymentID, pspReference string,
	amount int64, currency string,
	reason DisputeReason,
	deadline time.Time,
) (*Dispute, error) {
	if paymentID == "" {
		return nil, fmt.Errorf("payment_id is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}

	now := time.Now().UTC()
	d := &Dispute{
		id:           id,
		tenantID:     tenantID,
		paymentID:    paymentID,
		pspReference: pspReference,
		amount:       amount,
		currency:     currency,
		reason:       reason,
		status:       StatusOpen,
		deadline:     deadline,
		openedAt:     now,
	}

	d.record(DisputeOpenedEvent{
		baseEvent: newBase(tenantID.String()),
		DisputeID: id.String(),
		PaymentID: paymentID,
		TenantID:  tenantID.String(),
		Amount:    amount,
		Currency:  currency,
		Reason:    reason.String(),
	})

	return d, nil
}

// ReconstituteDispute reconstruye desde el repositorio.
func ReconstituteDispute(
	id DisputeID, tenantID TenantID,
	paymentID, pspReference string,
	amount int64, currency string,
	reason DisputeReason, status DisputeStatus,
	evidence []Evidence,
	responseNote, resolvedNote string,
	deadline, openedAt time.Time,
	respondedAt, resolvedAt *time.Time,
) *Dispute {
	return &Dispute{
		id: id, tenantID: tenantID,
		paymentID: paymentID, pspReference: pspReference,
		amount: amount, currency: currency,
		reason: reason, status: status,
		evidence:     evidence,
		responseNote: responseNote, resolvedNote: resolvedNote,
		deadline: deadline, openedAt: openedAt,
		respondedAt: respondedAt, resolvedAt: resolvedAt,
	}
}

// ── Transiciones del comercio ─────────────────────────────────────────────────

// Contest envía evidencia para contestar la disputa.
// El comercio tiene hasta deadline para hacerlo.
func (d *Dispute) Contest(evidence []Evidence, note string, now time.Time) error {
	if d.status != StatusOpen {
		return fmt.Errorf("%w: cannot contest from %q", ErrInvalidTransition, d.status)
	}
	if now.After(d.deadline) {
		return ErrDisputeExpired
	}
	if len(evidence) == 0 {
		return ErrEvidenceRequired
	}

	d.status = StatusUnderReview
	d.evidence = append(d.evidence, evidence...)
	d.responseNote = note
	d.respondedAt = &now
	return nil
}

// Accept acepta la disputa sin contestar (el comercio reconoce la devolución).
func (d *Dispute) Accept(note string) error {
	if d.status != StatusOpen {
		return fmt.Errorf("%w: cannot accept from %q", ErrInvalidTransition, d.status)
	}

	now := time.Now().UTC()
	d.status = StatusAccepted
	d.resolvedNote = note
	d.resolvedAt = &now

	d.record(DisputeResolvedEvent{
		baseEvent: newBase(d.tenantID.String()),
		DisputeID: d.id.String(),
		PaymentID: d.paymentID,
		TenantID:  d.tenantID.String(),
		Amount:    d.amount,
		Currency:  d.currency,
		Outcome:   OutcomeAccepted.String(),
	})

	return nil
}

// ── Transiciones del operador (resultado del banco) ───────────────────────────

// Resolve cierra la disputa con el resultado del banco.
// Solo aplica desde under_review.
func (d *Dispute) Resolve(outcome ResolutionOutcome, note string) error {
	if d.status != StatusUnderReview {
		return fmt.Errorf("%w: cannot resolve from %q", ErrInvalidTransition, d.status)
	}

	now := time.Now().UTC()
	switch outcome {
	case OutcomeWon:
		d.status = StatusWon
	case OutcomeLost:
		d.status = StatusLost
	default:
		return fmt.Errorf("%w: %q not valid for resolve", ErrInvalidResolutionOutcome, outcome)
	}

	d.resolvedNote = note
	d.resolvedAt = &now

	d.record(DisputeResolvedEvent{
		baseEvent: newBase(d.tenantID.String()),
		DisputeID: d.id.String(),
		PaymentID: d.paymentID,
		TenantID:  d.tenantID.String(),
		Amount:    d.amount,
		Currency:  d.currency,
		Outcome:   outcome.String(),
	})

	return nil
}

// Expire cierra la disputa por vencimiento del plazo.
// Solo aplica desde open.
func (d *Dispute) Expire() error {
	if d.status != StatusOpen {
		return fmt.Errorf("%w: cannot expire from %q", ErrInvalidTransition, d.status)
	}

	now := time.Now().UTC()
	d.status = StatusExpired
	d.resolvedAt = &now

	d.record(DisputeResolvedEvent{
		baseEvent: newBase(d.tenantID.String()),
		DisputeID: d.id.String(),
		PaymentID: d.paymentID,
		TenantID:  d.tenantID.String(),
		Amount:    d.amount,
		Currency:  d.currency,
		Outcome:   OutcomeExpired.String(),
	})

	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (d *Dispute) ID() DisputeID              { return d.id }
func (d *Dispute) TenantID() TenantID         { return d.tenantID }
func (d *Dispute) PaymentID() string          { return d.paymentID }
func (d *Dispute) PSPReference() string       { return d.pspReference }
func (d *Dispute) Amount() int64              { return d.amount }
func (d *Dispute) Currency() string           { return d.currency }
func (d *Dispute) Reason() DisputeReason      { return d.reason }
func (d *Dispute) Status() DisputeStatus      { return d.status }
func (d *Dispute) Evidence() []Evidence       { return d.evidence }
func (d *Dispute) ResponseNote() string       { return d.responseNote }
func (d *Dispute) ResolvedNote() string       { return d.resolvedNote }
func (d *Dispute) Deadline() time.Time        { return d.deadline }
func (d *Dispute) OpenedAt() time.Time        { return d.openedAt }
func (d *Dispute) RespondedAt() *time.Time    { return d.respondedAt }
func (d *Dispute) ResolvedAt() *time.Time     { return d.resolvedAt }

// IsOverdue retorna true si el plazo de respuesta venció y sigue abierta.
func (d *Dispute) IsOverdue(now time.Time) bool {
	return d.status == StatusOpen && now.After(d.deadline)
}

func (d *Dispute) PullEvents() []Event {
	evs := d.events
	d.events = nil
	return evs
}

func (d *Dispute) record(e Event) { d.events = append(d.events, e) }
