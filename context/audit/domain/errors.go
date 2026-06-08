package domain

import "errors"

var (
	ErrLogEntryNotFound  = errors.New("audit log entry not found")
	ErrChainBroken       = errors.New("audit chain integrity violation: hash mismatch detected")
	ErrInvalidAction     = errors.New("audit action cannot be empty")
	ErrInvalidResourceType = errors.New("invalid audit resource type")
)
