package domain

import "errors"

var (
	ErrEndpointNotFound    = errors.New("webhook endpoint not found")
	ErrEndpointInactive    = errors.New("webhook endpoint is inactive")
	ErrEndpointURLEmpty    = errors.New("webhook endpoint URL cannot be empty")
	ErrNoEventsSubscribed  = errors.New("must subscribe to at least one event type")
	ErrDeliveryNotFound    = errors.New("webhook delivery not found")
	ErrDeliveryNotRetryable = errors.New("delivery cannot be retried: already delivered or exhausted")
	ErrDuplicateDelivery   = errors.New("delivery already exists for this event")
)
