package domain

import "errors"

var (
	ErrNotificationNotFound    = errors.New("notification not found")
	ErrNoRecipientEmail        = errors.New("no recipient email found for tenant")
	ErrTemplateNotFound        = errors.New("no template registered for this event type")
	ErrInvalidChannel          = errors.New("invalid notification channel")
	ErrPreferenceNotFound      = errors.New("notification preference not found")
	ErrSendFailed              = errors.New("failed to send notification")
)
