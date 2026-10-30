package application

// SendNotificationCmd es el comando interno usado por el consumer NATS.
type SendNotificationCmd struct {
	TenantID     string
	EventType    string            // ej: "payment.captured.v1" (se normaliza internamente)
	TemplateData map[string]string // datos para renderizar el template
	NatsMsgID    string            // ID del mensaje NATS (Nats-Msg-Id) para dedupe/idempotencia
}

// UpdatePreferenceCmd actualiza una preferencia de notificación.
//
// RecipientEmail es *string para distinguir "campo omitido" (nil → mantener
// el override existente, merge) de "campo presente vacío" ("" → limpiar el
// override y volver al email del admin). Sin el puntero, un PUT parcial
// écrasaba el email guardado aunque el cliente no lo enviara.
type UpdatePreferenceCmd struct {
	TenantID       string
	EventType      string
	Enabled        bool
	RecipientEmail *string
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
