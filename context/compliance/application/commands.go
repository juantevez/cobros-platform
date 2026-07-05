package application

// ── Commands ──────────────────────────────────────────────────────────────────

// ScreenApplicationCmd dispara el screening de un onboarding contra la watchlist.
type ScreenApplicationCmd struct {
	TenantID      string
	ApplicationID string
	LegalName     string
}

// MonitorTransactionCmd dispara las reglas de monitoreo sobre un pago capturado.
type MonitorTransactionCmd struct {
	TenantID      string
	PaymentID     string
	Amount        int64
	Currency      string
	PaymentMethod string
}

// ResolveAlertCmd dispone una alerta (revisión manual).
type ResolveAlertCmd struct {
	TenantID    string
	AlertID     string
	Disposition string // "cleared" | "confirmed"
	Note        string
}

// AddWatchlistEntryCmd agrega una entrada a la watchlist global (admin).
type AddWatchlistEntryCmd struct {
	FullName string
	ListType string
	Country  string
	Source   string
}

// ── Queries ───────────────────────────────────────────────────────────────────

type ListAlertsQuery struct {
	TenantID     string
	StatusFilter string // "" | "open" | "cleared" | "confirmed"
	Limit        int
}

type GetAlertQuery struct {
	TenantID string
	AlertID  string
}

// ── Views ─────────────────────────────────────────────────────────────────────

type AlertView struct {
	ID         string            `json:"id"`
	TenantID   string            `json:"tenant_id"`
	AlertType  string            `json:"alert_type"`
	RiskLevel  string            `json:"risk_level"`
	Status     string            `json:"status"`
	Subject    string            `json:"subject"`
	Score      int               `json:"score"`
	Details    map[string]string `json:"details,omitempty"`
	Note       string            `json:"note,omitempty"`
	CreatedAt  string            `json:"created_at"`
	ResolvedAt *string           `json:"resolved_at,omitempty"`
}

type WatchlistEntryView struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	ListType string `json:"list_type"`
	Country  string `json:"country,omitempty"`
	Source   string `json:"source,omitempty"`
}
