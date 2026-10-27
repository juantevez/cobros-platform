package domain

import "errors"

var (
	ErrDisputeNotFound          = errors.New("dispute not found")
	ErrDisputeAlreadyClosed     = errors.New("dispute is already closed")
	ErrInvalidTransition        = errors.New("invalid dispute status transition")
	ErrEvidenceRequired         = errors.New("evidence is required to contest a dispute")
	ErrDisputeExpired           = errors.New("dispute response deadline has passed")
	ErrPaymentNotCaptured       = errors.New("can only dispute captured payments")
	ErrDuplicateDispute         = errors.New("a dispute already exists for this payment")
	ErrInvalidDisputeReason     = errors.New("invalid dispute reason")
	ErrInvalidResolutionOutcome = errors.New("invalid resolution outcome")
	// ErrInvalidDeadline: el deadline provisto es cero o ya venció al abrir la disputa.
	ErrInvalidDeadline = errors.New("invalid dispute deadline")
	// ErrAmountExceedsPayment: el monto disputado supera al capturado en el pago.
	ErrAmountExceedsPayment = errors.New("disputed amount exceeds captured payment amount")
	// ErrCurrencyMismatch: la moneda de la disputa difiere de la del pago.
	ErrCurrencyMismatch = errors.New("dispute currency does not match payment currency")
	// ErrInvalidEvidenceType: el tipo de evidencia no está en el vocabulario permitido.
	ErrInvalidEvidenceType = errors.New("invalid evidence type")
	// ErrOptimisticLock: otra operación modificó la disputa concurrentemente.
	ErrOptimisticLock = errors.New("dispute was modified concurrently, please retry")
)
