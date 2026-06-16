package domain

import "errors"

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
)
