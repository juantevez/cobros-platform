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
)
