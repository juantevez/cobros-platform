package domain

import "errors"

var (
	ErrNoRecipientEmail   = errors.New("no recipient email found for tenant")
	ErrInvalidChannel     = errors.New("invalid notification channel")
	ErrPreferenceNotFound = errors.New("notification preference not found")
	ErrInvalidTenantID    = errors.New("invalid tenant id")
	ErrInvalidStatus      = errors.New("invalid notification status")
	// ErrNotificationDuplicate indica que ya existe un registro para la
	// clave natural de idempotencia (tenant_id, event_type, nats_msg_id):
	// es la reentrega de un evento ya procesado, no un error real.
	ErrNotificationDuplicate = errors.New("notification already processed (duplicate event delivery)")
)
