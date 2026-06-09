package domain

import (
	"fmt"
	"time"
)

// Payment es el agregado raíz del contexto Payment Processing.
//
// Gestiona el ciclo de vida de un cobro desde su creación hasta la
// captura de fondos o fallo. Es append-friendly: se persiste en cada
// transición de estado.
//
// FSM:
//
//	initiated → processing → captured   (camino feliz)
//	                      → failed      (PSP rechaza)
//	initiated → risk_rejected           (riesgo rechaza antes de ir al PSP)
//	captured  → refunded                (reembolso)
//
// El aggregate no llama al Ledger directamente. Emite PaymentCaptured y
// el Ledger tiene un consumer que crea el asiento contable (consistencia eventual).
type Payment struct {
	id             PaymentID
	tenantID       TenantID
	idempotencyKey string
	checkoutID     string // referencia al Checkout (Fase 3); vacío en Fase 2
	amount         Money
	platformFee    Money  // calculado al capturar
	pspFee         Money  // informado por el PSP
	payerInfo      PayerInfo
	method         PaymentMethod
	pspName        string // "mock", "mercadopago", "paypal"
	pspReference   string // ID de la transacción en el PSP
	status         PaymentStatus
	failureReason  string
	metadata       map[string]string
	authorizedAt   *time.Time
	capturedAt     *time.Time
	failedAt       *time.Time
	createdAt      time.Time
	updatedAt      time.Time

	events []Event
}

// NewPayment crea un Payment en estado Initiated.
func NewPayment(
	id PaymentID,
	tenantID TenantID,
	idempotencyKey string,
	amount Money,
	payerInfo PayerInfo,
	method PaymentMethod,
	metadata map[string]string,
) (*Payment, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency key is required")
	}

	now := time.Now().UTC()
	zeroMoney := Money{amount: 0, currency: amount.Currency()}

	return &Payment{
		id:             id,
		tenantID:       tenantID,
		idempotencyKey: idempotencyKey,
		amount:         amount,
		platformFee:    zeroMoney,
		pspFee:         zeroMoney,
		payerInfo:      payerInfo,
		method:         method,
		status:         StatusInitiated,
		metadata:       metadata,
		createdAt:      now,
		updatedAt:      now,
	}, nil
}

// ReconstitutePayment reconstruye un Payment desde el repositorio.
func ReconstitutePayment(
	id PaymentID, tenantID TenantID,
	idempotencyKey, checkoutID string,
	amount, platformFee, pspFee Money,
	payerInfo PayerInfo,
	method PaymentMethod,
	pspName, pspReference string,
	status PaymentStatus,
	failureReason string,
	metadata map[string]string,
	authorizedAt, capturedAt, failedAt *time.Time,
	createdAt, updatedAt time.Time,
) *Payment {
	return &Payment{
		id: id, tenantID: tenantID,
		idempotencyKey: idempotencyKey, checkoutID: checkoutID,
		amount: amount, platformFee: platformFee, pspFee: pspFee,
		payerInfo: payerInfo, method: method,
		pspName: pspName, pspReference: pspReference,
		status: status, failureReason: failureReason,
		metadata: metadata,
		authorizedAt: authorizedAt, capturedAt: capturedAt, failedAt: failedAt,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// ── Transiciones ──────────────────────────────────────────────────────────────

// MarkProcessing indica que el pago fue enviado al PSP.
func (p *Payment) MarkProcessing() error {
	if p.status != StatusInitiated {
		return fmt.Errorf("%w: cannot mark processing from %q", ErrInvalidTransition, p.status)
	}
	p.status = StatusProcessing
	p.updatedAt = time.Now().UTC()
	return nil
}

// Capture registra la captura exitosa de fondos por el PSP.
// platformFee y pspFee son las comisiones calculadas.
func (p *Payment) Capture(pspName, pspReference string, platformFee, pspFee Money) error {
	if p.status != StatusProcessing {
		return fmt.Errorf("%w: cannot capture from %q", ErrInvalidTransition, p.status)
	}

	now := time.Now().UTC()
	p.status = StatusCaptured
	p.pspName = pspName
	p.pspReference = pspReference
	p.platformFee = platformFee
	p.pspFee = pspFee
	p.capturedAt = &now
	p.updatedAt = now

	p.record(PaymentCapturedEvent{
		baseEvent:      newBase(p.tenantID.String()),
		PaymentID:      p.id.String(),
		TenantID:       p.tenantID.String(),
		Amount:         p.amount.Amount(),
		Currency:       p.amount.Currency(),
		PlatformFee:    platformFee.Amount(),
		PSPFee:         pspFee.Amount(),
		PaymentMethod:  p.method.String(),
		PSPReference:   pspReference,
		IdempotencyKey: p.idempotencyKey,
	})

	return nil
}

// RejectByRisk marca el pago como rechazado por el evaluador de riesgo.
func (p *Payment) RejectByRisk(reason string) error {
	if p.status != StatusInitiated {
		return fmt.Errorf("%w: cannot reject by risk from %q", ErrInvalidTransition, p.status)
	}

	now := time.Now().UTC()
	p.status = StatusRiskRejected
	p.failureReason = reason
	p.failedAt = &now
	p.updatedAt = now

	p.record(PaymentFailedEvent{
		baseEvent:     newBase(p.tenantID.String()),
		PaymentID:     p.id.String(),
		TenantID:      p.tenantID.String(),
		Amount:        p.amount.Amount(),
		Currency:      p.amount.Currency(),
		FailureReason: reason,
		PaymentMethod: p.method.String(),
	})

	return nil
}

// Fail marca el pago como fallido por el PSP.
func (p *Payment) Fail(pspName, reason string) error {
	if p.status != StatusProcessing {
		return fmt.Errorf("%w: cannot fail from %q", ErrInvalidTransition, p.status)
	}

	now := time.Now().UTC()
	p.status = StatusFailed
	p.pspName = pspName
	p.failureReason = reason
	p.failedAt = &now
	p.updatedAt = now

	p.record(PaymentFailedEvent{
		baseEvent:     newBase(p.tenantID.String()),
		PaymentID:     p.id.String(),
		TenantID:      p.tenantID.String(),
		Amount:        p.amount.Amount(),
		Currency:      p.amount.Currency(),
		FailureReason: reason,
		PaymentMethod: p.method.String(),
	})

	return nil
}

// Refund marca el pago como reembolsado.
func (p *Payment) Refund(pspReference string) error {
	if p.status != StatusCaptured {
		return ErrNotCaptured
	}

	p.status = StatusRefunded
	p.updatedAt = time.Now().UTC()

	p.record(PaymentRefundedEvent{
		baseEvent:    newBase(p.tenantID.String()),
		PaymentID:    p.id.String(),
		TenantID:     p.tenantID.String(),
		RefundAmount: p.amount.Amount(),
		Currency:     p.amount.Currency(),
		PSPReference: pspReference,
	})

	return nil
}

// NetAmount retorna el monto neto para el comercio (amount - platformFee - pspFee).
func (p *Payment) NetAmount() int64 {
	return p.amount.Amount() - p.platformFee.Amount() - p.pspFee.Amount()
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (p *Payment) ID() PaymentID            { return p.id }
func (p *Payment) TenantID() TenantID       { return p.tenantID }
func (p *Payment) IdempotencyKey() string   { return p.idempotencyKey }
func (p *Payment) CheckoutID() string       { return p.checkoutID }
func (p *Payment) Amount() Money            { return p.amount }
func (p *Payment) PlatformFee() Money       { return p.platformFee }
func (p *Payment) PSPFee() Money            { return p.pspFee }
func (p *Payment) PayerInfo() PayerInfo     { return p.payerInfo }
func (p *Payment) Method() PaymentMethod    { return p.method }
func (p *Payment) PSPName() string          { return p.pspName }
func (p *Payment) PSPReference() string     { return p.pspReference }
func (p *Payment) Status() PaymentStatus    { return p.status }
func (p *Payment) FailureReason() string    { return p.failureReason }
func (p *Payment) Metadata() map[string]string { return p.metadata }
func (p *Payment) AuthorizedAt() *time.Time { return p.authorizedAt }
func (p *Payment) CapturedAt() *time.Time   { return p.capturedAt }
func (p *Payment) FailedAt() *time.Time     { return p.failedAt }
func (p *Payment) CreatedAt() time.Time     { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time     { return p.updatedAt }

func (p *Payment) PullEvents() []Event {
	evs := p.events
	p.events = nil
	return evs
}

func (p *Payment) record(e Event) { p.events = append(p.events, e) }
