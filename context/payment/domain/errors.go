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
	ErrAlreadyCaptured      = errors.New("payment is already captured")
	ErrNotCaptured          = errors.New("payment is not captured; cannot refund")
	ErrRefundExceedsAmount  = errors.New("refund amount exceeds original payment amount")
)
