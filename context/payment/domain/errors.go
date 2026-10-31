package domain

import "errors"

// FailureDeclinedByPSP es el motivo público de un rechazo del PSP: el error
// crudo puede filtrar datos del PSP o de la tarjeta.
const FailureDeclinedByPSP = "declined by the payment provider"

var (
	// ErrInvalidID: un ID (pago, tenant) que no es un UUID.
	ErrInvalidID            = errors.New("invalid id: must be a UUID")
	ErrPaymentNotFound      = errors.New("payment not found")
	ErrPaymentAlreadyExists = errors.New("payment with this idempotency key already exists")
	// ErrIdempotencyKeyConflict: la clave ya se usó para un pago con otro
	// monto, moneda o método.
	// ErrInvalidFee: una comisión negativa, o comisiones que dejan el neto del
	// comercio en cero o negativo (Ledger no puede registrar ese asiento).
	ErrInvalidFee             = errors.New("invalid fee: fees must be non-negative and below the payment amount")
	ErrIdempotencyKeyConflict = errors.New("idempotency key already used with a different amount, currency or payment method")
	ErrInvalidTransition      = errors.New("invalid payment status transition")
	ErrInvalidAmount          = errors.New("payment amount must be greater than zero")
	ErrInvalidCurrency        = errors.New("invalid currency code")
	ErrInvalidMethod          = errors.New("invalid payment method")
	ErrRiskRejected           = errors.New("payment rejected by risk evaluation")
	ErrPaymentDeclined        = errors.New("payment declined by the payment provider")
	// ErrPaymentPending: el PSP no confirmó el resultado (timeout, error de
	// red). El pago queda en processing hasta que se resuelva.
	ErrPaymentPending = errors.New("payment outcome is pending confirmation from the payment provider")
	// ErrPaymentChanged: el pago ya no estaba en el estado del que partía la
	// transición (otra ejecución lo resolvió antes).
	ErrPaymentChanged      = errors.New("payment was modified concurrently")
	ErrTenantNotActive     = errors.New("tenant is not enabled to process payments")
	ErrAlreadyCaptured     = errors.New("payment is already captured")
	ErrNotCaptured         = errors.New("payment is not captured; cannot refund")
	ErrRefundExceedsAmount = errors.New("refund amount exceeds original payment amount")
)
