package domain

import "errors"

var (
	// ErrInvalidID: un ID (endpoint, delivery, tenant) que no es un UUID.
	ErrInvalidID          = errors.New("invalid id: must be a UUID")
	ErrEndpointNotFound   = errors.New("webhook endpoint not found")
	ErrEndpointInactive   = errors.New("webhook endpoint is inactive")
	ErrEndpointURLEmpty   = errors.New("webhook endpoint URL cannot be empty")
	ErrInvalidEndpointURL = errors.New("invalid webhook URL")
	// ErrDestinationNotAllowed: la URL apunta a la red interna (SSRF).
	ErrDestinationNotAllowed = errors.New("webhook destination not allowed: must be a public address")
	ErrUnknownEventType      = errors.New("unknown event type")
	ErrNoEventsSubscribed    = errors.New("must subscribe to at least one event type")
	ErrDeliveryNotFound      = errors.New("webhook delivery not found")
	ErrDeliveryNotRetryable  = errors.New("delivery cannot be retried: already delivered or exhausted")
	ErrDuplicateDelivery     = errors.New("delivery already exists for this event")
	// ErrDeliveryInProgress: otro proceso tiene reclamada la delivery o la
	// actualizó mientras se despachaba.
	ErrDeliveryInProgress = errors.New("delivery is being dispatched by another process")
)
