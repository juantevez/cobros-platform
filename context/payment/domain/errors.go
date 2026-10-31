package domain

import "errors"

var (
	ErrPaymentNotFound      = errors.New("payment not found")
	ErrPaymentAlreadyExists = errors.New("payment with this idempotency key already exists")
	ErrInvalidTransition    = errors.New("invalid payment status transition")
	ErrInvalidAmount        = errors.New("payment amount must be greater than zero")
	ErrInvalidCurrency      = errors.New("invalid currency code")
	ErrInvalidMethod        = errors.New("invalid payment method")
	ErrRiskRejected         = errors.New("payment rejected by risk evaluation")
	ErrPaymentDeclined      = errors.New("payment declined by the payment provider")
	// ErrPaymentPending: el PSP no confirmó el resultado (timeout, error de
	// red). El pago queda en processing hasta que se resuelva.
	ErrPaymentPending = errors.New("payment outcome is pending confirmation from the payment provider")
	// ErrPaymentChanged: el pago ya no estaba en el estado del que partía la
	// transición (otra ejecución lo resolvió antes).
	ErrPaymentChanged = errors.New("payment was modified concurrently")
	ErrTenantNotActive      = errors.New("tenant is not enabled to process payments")
	ErrAlreadyCaptured      = errors.New("payment is already captured")
	ErrNotCaptured          = errors.New("payment is not captured; cannot refund")
	ErrRefundExceedsAmount  = errors.New("refund amount exceeds original payment amount")
)
