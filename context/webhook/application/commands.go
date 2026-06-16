package application

import "time"

// RegisterEndpointCmd registra un nuevo endpoint webhook para un tenant.
type RegisterEndpointCmd struct {
	TenantID    string
	URL         string
	Events      []string // ej: ["payment.captured", "payout.confirmed"]
	Description string
}

type RegisterEndpointResult struct {
	EndpointID string
	// Secret se entrega solo aquí, una única vez. El comercio debe guardarlo.
	Secret     string
	SecretHint string
}

// DeactivateEndpointCmd desactiva un endpoint.
type DeactivateEndpointCmd struct {
	TenantID   string
	EndpointID string
}

// GetDeliveryQuery consulta una delivery con todos sus intentos.
type GetDeliveryQuery struct {
	TenantID   string
	DeliveryID string
}

// ListDeliveriesQuery lista deliveries del tenant.
type ListDeliveriesQuery struct {
	TenantID string
	Limit    int
}

// RetryDeliveryCmd fuerza un reintento manual de una delivery fallida.
type RetryDeliveryCmd struct {
	TenantID   string
	DeliveryID string
}

// ── Views ─────────────────────────────────────────────────────────────────────

type EndpointView struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	URL         string   `json:"url"`
	SecretHint  string   `json:"secret_hint"`
	Events      []string `json:"events"`
	Active      bool     `json:"active"`
	Description string   `json:"description,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

type DeliveryView struct {
	ID           string          `json:"id"`
	EndpointID   string          `json:"endpoint_id"`
	EventType    string          `json:"event_type"`
	EventID      string          `json:"event_id"`
	Status       string          `json:"status"`
	AttemptCount int             `json:"attempt_count"`
	NextRetryAt  *time.Time      `json:"next_retry_at,omitempty"`
	Attempts     []AttemptView   `json:"attempts,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

type AttemptView struct {
	AttemptNum   int    `json:"attempt_num"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
	Error        string `json:"error,omitempty"`
	DurationMs   int64  `json:"duration_ms"`
	AttemptedAt  string `json:"attempted_at"`
}
