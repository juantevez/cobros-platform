package domain

import "errors"

var (
	ErrNotificationNotFound    = errors.New("notification not found")
	ErrNoRecipientEmail        = errors.New("no recipient email found for tenant")
	ErrTemplateNotFound        = errors.New("no template registered for this event type")
	ErrInvalidChannel          = errors.New("invalid notification channel")
	ErrPreferenceNotFound      = errors.New("notification preference not found")
	ErrSendFailed              = errors.New("failed to send notification")
	// ErrNotificationDuplicate indica que ya existe un registro para la
	// clave natural de idempotencia (tenant_id, event_type, nats_msg_id):
	// es la reentrega de un evento ya procesado, no un error real.
	ErrNotificationDuplicate = errors.New("notification already processed (duplicate event delivery)")
)
