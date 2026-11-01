package domain

import "errors"

// Motivos públicos de un payout fallido o ambiguo: el error crudo del banco
// puede traer datos de la cuenta o del banco y va a bank_failure_detail.
const (
	FailureRejectedByBank    = "rejected by the bank"
	FailureUnconfirmedByBank = "the bank did not confirm the transfer"
)

var (
	ErrPayoutNotFound         = errors.New("payout not found")
	ErrInsufficientBalance    = errors.New("insufficient available balance for payout")
	ErrInvalidAmount          = errors.New("payout amount must be greater than zero")
	ErrInvalidCurrency        = errors.New("invalid currency code")
	ErrInvalidTransition      = errors.New("invalid payout status transition")
	ErrNoBankAccount          = errors.New("tenant has no verified bank account for payouts")
	ErrPayoutAlreadyConfirmed = errors.New("payout is already confirmed")
	ErrPayoutAlreadyFailed    = errors.New("payout already failed")
	ErrFailureReasonRequired  = errors.New("failure reason is required")

	// ErrPayoutAlreadyExists indica que ya existe un payout del tenant con la
	// misma idempotency key. El caso de uso lo resuelve retornando el payout
	// existente; el repositorio lo mapea desde un violation de UNIQUE.
	ErrPayoutAlreadyExists = errors.New("payout with this idempotency key already exists")

	// ErrBankTransferUnknown indica que el resultado de la transferencia es
	// indeterminado (timeout, error de red, respuesta ambigua del banco).
	// NO debe tratarse como fallo: los fondos pueden haber sido acreditados.
	ErrBankTransferUnknown = errors.New("bank transfer result unknown")
	// ErrPayoutRejected: el banco rechazó la transferencia (el payout queda
	// failed y el Ledger revierte el asiento). Mensaje genérico: el detalle
	// del banco no se expone.
	ErrPayoutRejected = errors.New("payout rejected by the bank")

	// ErrIdempotencyKeyConflict: la clave reutilizada corresponde a un payout
	// con monto o moneda distintos.
	ErrIdempotencyKeyConflict = errors.New("idempotency key reused with different amount or currency")

	// ErrIdempotencyKeyRequired: todo desembolso debe traer clave de idempotencia.
	ErrIdempotencyKeyRequired = errors.New("payout idempotency key is required")

	// ErrPayoutsNotAllowed: el comercio no puede desembolsar (no activo,
	// suspendido o en hold preventivo). El mensaje es genérico a propósito:
	// no debe revelar un hold de cumplimiento (tipping-off).
	ErrPayoutsNotAllowed = errors.New("payouts are not available for this account")
	// ErrPayoutChanged: el payout ya no estaba en el estado del que partía la
	// transición (otra ejecución lo resolvió antes).
	ErrPayoutChanged = errors.New("payout was modified concurrently")
)
