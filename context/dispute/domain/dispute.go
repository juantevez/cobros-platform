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
	id             DisputeID
	tenantID       TenantID
	paymentID      string // referencia al pago disputado
	pspReference   string // referencia del PSP para la disputa
	amount         int64  // monto disputado en centavos
	capturedAmount int64  // monto capturado del pago al momento de disputar
	currency       string
	reason         DisputeReason
	status         DisputeStatus
	evidence       []Evidence
	responseNote   string    // nota del comercio al contestar
	resolvedNote   string    // nota del operador al cerrar
	deadline       time.Time // fecha límite para contestar
	openedAt       time.Time
	version        int64 // concurrencia optimista (columna `version` en DB)
	respondedAt    *time.Time
	resolvedAt     *time.Time

	events []Event
}

// NewDispute crea una disputa en estado Open.
// Se llama cuando el banco/PSP notifica al sistema sobre una nueva disputa.
// payment es el DTO del pago disputado (leído por el caso de uso vía
// PaymentReader); concentra tres invariantes cross-context antes de mutar:
// el pago debe existir, pertenecer al tenant de la disputa y estar capturado.
//
// now se inyecta desde la capa de aplicación (Clock): una disputa cuyo deadline
// ya venció no puede crearse (ErrInvalidDeadline), y el openedAt queda anclado
// al mismo reloj que el resto del ciclo de vida.
func NewDispute(
	id DisputeID,
	tenantID TenantID,
	payment PaymentRef,
	pspReference string,
	amount int64, currency string,
	reason DisputeReason,
	deadline time.Time,
	now time.Time,
) (*Dispute, error) {
	if payment.ID == "" {
		return nil, fmt.Errorf("payment_id is required")
	}
	if payment.Status != "captured" {
		// refunded/failed/etc.: no hay fondos del comercio que congelar.
		return nil, fmt.Errorf("%w: payment status is %q", ErrPaymentNotCaptured, payment.Status)
	}
	if payment.TenantID != "" && string(tenantID) != payment.TenantID {
		// El pago pertenece a otra aplicación: la disputa debe atribuirse al dueño
		// del pago (el caller no puede "regalar" disputas a terceros ni roer el
		// saldo de otro tenant).
		tenantID = TenantID(payment.TenantID)
	}
	if amount > payment.Amount {
		return nil, fmt.Errorf("%w: disputed amount %d exceeds captured amount %d",
			ErrAmountExceedsPayment, amount, payment.Amount)
	}
	if currency != "" && payment.Currency != "" && currency != payment.Currency {
		return nil, fmt.Errorf("%w: dispute currency %q != payment currency %q",
			ErrCurrencyMismatch, currency, payment.Currency)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if deadline.IsZero() {
		return nil, fmt.Errorf("%w: deadline is required", ErrInvalidDeadline)
	}
	if !deadline.After(now) {
		return nil, fmt.Errorf("%w: deadline %s already passed",
			ErrInvalidDeadline, deadline.UTC().Format(time.RFC3339))
	}

	now = now.UTC()
	d := &Dispute{
		id:             id,
		tenantID:       tenantID,
		paymentID:      payment.ID,
		pspReference:   pspReference,
		amount:         amount,
		capturedAmount: payment.Amount,
		currency:       currency,
		reason:         reason,
		status:         StatusOpen,
		deadline:       deadline,
		openedAt:       now,
		version:        1,
	}

	d.record(DisputeOpenedEvent{
		baseEvent: newBase(tenantID.String()),
		DisputeID: id.String(),
		PaymentID: payment.ID,
		TenantID:  tenantID.String(),
		Amount:    amount,
		Currency:  currency,
		Reason:    reason.String(),
	})

	return d, nil
}

// ReconstituteDispute reconstruye desde el repositorio.
// loadedVersion es la versión persistida leída por el repositorio; cada
// transición exitosa la incrementa y Update la usa como guard de concurrencia
// optimista (UPDATE ... WHERE id = $1 AND version = <esperada>).
func ReconstituteDispute(
	id DisputeID, tenantID TenantID,
	paymentID, pspReference string,
	amount int64, currency string,
	reason DisputeReason, status DisputeStatus,
	evidence []Evidence,
	responseNote, resolvedNote string,
	deadline, openedAt time.Time,
	respondedAt, resolvedAt *time.Time,
	loadedVersion int64,
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
		version: loadedVersion,
		// El monto capturado no se persiste en la fila (es una foto del pago al
		// momento de abrir la disputa): al reconstituir se toma como referencia
		// el propio monto disputado, que nunca lo excede (validado en NewDispute).
		capturedAmount: amount,
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
	d.version++
	d.evidence = append(d.evidence, evidence...)
	d.responseNote = note
	d.respondedAt = &now
	return nil
}

// Accept acepta la disputa sin contestar (el comercio reconoce la devolución).
// A diferencia de Contest, el plazo no es una guard: el comercio puede aceptar
// una disputa aunque haya vencido el deadline (de hecho suele ser la decisión
// natural del que nunca presentó evidencia); el poller cierra como expired solo
// las que siguen abiertas.
func (d *Dispute) Accept(note string, now time.Time) error {
	if d.status != StatusOpen {
		return fmt.Errorf("%w: cannot accept from %q", ErrInvalidTransition, d.status)
	}

	now = now.UTC()
	d.status = StatusAccepted
	d.version++
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
func (d *Dispute) Resolve(outcome ResolutionOutcome, note string, now time.Time) error {
	if d.status != StatusUnderReview {
		return fmt.Errorf("%w: cannot resolve from %q", ErrInvalidTransition, d.status)
	}

	now = now.UTC()
	switch outcome {
	case OutcomeWon:
		d.status = StatusWon
	case OutcomeLost:
		d.status = StatusLost
	default:
		return fmt.Errorf("%w: %q not valid for resolve", ErrInvalidResolutionOutcome, outcome)
	}
	d.version++

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
func (d *Dispute) Expire(now time.Time) error {
	if d.status != StatusOpen {
		return fmt.Errorf("%w: cannot expire from %q", ErrInvalidTransition, d.status)
	}

	now = now.UTC()
	d.status = StatusExpired
	d.version++
	d.resolvedNote = "response deadline elapsed without merchant action"
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

// CanAddEvidence permite acumular evidencia adicional mientras la disputa está
// bajo revisión y dentro del plazo. No cambia el estado ni emite eventos: es
// documentación complementaria para el banco.
func (d *Dispute) CanAddEvidence(evidence []Evidence, now time.Time) error {
	if d.status != StatusUnderReview {
		return fmt.Errorf("%w: cannot add evidence from %q", ErrInvalidTransition, d.status)
	}
	if len(evidence) == 0 {
		return ErrEvidenceRequired
	}
	if now.After(d.deadline) {
		return ErrDisputeExpired
	}
	return nil
}

// AddEvidence valida y agrega evidencia extra (ver CanAddEvidence).
func (d *Dispute) AddEvidence(evidence []Evidence, now time.Time) error {
	if err := d.CanAddEvidence(evidence, now); err != nil {
		return err
	}
	d.evidence = append(d.evidence, evidence...)
	d.version++
	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (d *Dispute) ID() DisputeID        { return d.id }
func (d *Dispute) TenantID() TenantID   { return d.tenantID }
func (d *Dispute) PaymentID() string    { return d.paymentID }
func (d *Dispute) PSPReference() string { return d.pspReference }
func (d *Dispute) Amount() int64        { return d.amount }

// CapturedAmount retorna el monto capturado del pago en el momento de abrir la disputa.
func (d *Dispute) CapturedAmount() int64   { return d.capturedAmount }
func (d *Dispute) Currency() string        { return d.currency }
func (d *Dispute) Reason() DisputeReason   { return d.reason }
func (d *Dispute) Status() DisputeStatus   { return d.status }
func (d *Dispute) Evidence() []Evidence    { return d.evidence }
func (d *Dispute) ResponseNote() string    { return d.responseNote }
func (d *Dispute) ResolvedNote() string    { return d.resolvedNote }
func (d *Dispute) Deadline() time.Time     { return d.deadline }
func (d *Dispute) OpenedAt() time.Time     { return d.openedAt }
func (d *Dispute) RespondedAt() *time.Time { return d.respondedAt }
func (d *Dispute) ResolvedAt() *time.Time  { return d.resolvedAt }

// Version retorna la versión actual del agregado. Los casos de uso la pasan al
// repositorio como expectedVersion (concurrencia optimista).
func (d *Dispute) Version() int64 { return d.version }

// IsOverdue retorna true si el plazo de respuesta venció y la disputa sigue
// sin resolución. Las disputas cerradas por el banco (won/lost) nunca están
// vencidas: el deadline dejó de importar en el momento del cierre. En cambio,
// una disputa aceptada o expirada después de vencido el plazo sí lo estuvo:
// "overdue" alimenta el SLA de respuesta del comercio, y cerrar tarde no borra
// que se respondió fuera de término.
func (d *Dispute) IsOverdue(now time.Time) bool {
	switch d.status {
	case StatusWon, StatusLost:
		return false
	default:
		return now.After(d.deadline)
	}
}

func (d *Dispute) PullEvents() []Event {
	evs := d.events
	d.events = nil
	return evs
}

func (d *Dispute) record(e Event) { d.events = append(d.events, e) }
