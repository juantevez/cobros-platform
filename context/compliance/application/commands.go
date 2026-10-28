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

// ResolveAlertCmd es la disposición del primer analista de cumplimiento.
// En alertas four-eyes queda como propuesta (pending_second_approval).
type ResolveAlertCmd struct {
	AlertID     string
	Disposition string // "cleared" | "confirmed"
	Note        string
	Analyst     string // actor autenticado (userID) que dispone
}

// ReviewProposalCmd es la segunda intervención four-eyes: aprobar o rechazar
// la propuesta del primer analista.
type ReviewProposalCmd struct {
	AlertID string
	Note    string
	Analyst string // debe ser distinto de quien propuso
}

// AddWatchlistEntryCmd agrega una entrada a la watchlist global (admin).
type AddWatchlistEntryCmd struct {
	FullName string
	ListType string
	Country  string
	Source   string
}

// RemoveWatchlistEntryCmd da de baja una entrada de la watchlist global (admin).
type RemoveWatchlistEntryCmd struct {
	EntryID string
}

// ── Queries ───────────────────────────────────────────────────────────────────

// ListAlertsQuery lista alertas de toda la plataforma (revisión por
// cumplimiento); TenantID opcional acota a un comercio.
type ListAlertsQuery struct {
	TenantID     string // "" = todos los comercios
	StatusFilter string // "" | "open" | "pending_second_approval" | "cleared" | "confirmed"
	Limit        int
}

type GetAlertQuery struct {
	AlertID string
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
	ResolvedBy string            `json:"resolved_by,omitempty"`
	CreatedAt  string            `json:"created_at"`
	ResolvedAt *string           `json:"resolved_at,omitempty"`

	// Four-eyes
	RequiresSecondApproval bool    `json:"requires_second_approval"`
	ProposedStatus         string  `json:"proposed_status,omitempty"`
	ProposedBy             string  `json:"proposed_by,omitempty"`
	ProposedAt             *string `json:"proposed_at,omitempty"`
	ApprovalNote           string  `json:"approval_note,omitempty"`
}

type WatchlistEntryView struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	ListType string `json:"list_type"`
	Country  string `json:"country,omitempty"`
	Source   string `json:"source,omitempty"`
}
