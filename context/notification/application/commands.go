package application

// SendNotificationCmd es el comando interno usado por el consumer NATS.
type SendNotificationCmd struct {
	TenantID       string
	EventType      string            // ej: "payment.captured.v1"
	TemplateData   map[string]string // datos para renderizar el template
}

// UpdatePreferenceCmd actualiza una preferencia de notificación.
type UpdatePreferenceCmd struct {
	TenantID       string
	EventType      string
	Enabled        bool
	RecipientEmail string // vacío = usar email del admin
}

// GetPreferencesQuery consulta las preferencias de un tenant.
type GetPreferencesQuery struct {
	TenantID string
}

// ── Views ─────────────────────────────────────────────────────────────────────

type NotificationView struct {
	ID             string `json:"id"`
	EventType      string `json:"event_type"`
	Channel        string `json:"channel"`
	RecipientEmail string `json:"recipient_email"`
	Subject        string `json:"subject"`
	Status         string `json:"status"`
	ErrorMsg       string `json:"error_msg,omitempty"`
	SentAt         string `json:"sent_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type PreferenceView struct {
	EventType      string `json:"event_type"`
	Channel        string `json:"channel"`
	Enabled        bool   `json:"enabled"`
	RecipientEmail string `json:"recipient_email,omitempty"`
}
