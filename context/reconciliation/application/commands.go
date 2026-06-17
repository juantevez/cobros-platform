package application

import "time"

// StartReconciliationCmd inicia un nuevo run de reconciliación.
type StartReconciliationCmd struct {
	TenantID   string // vacío para reconciliación global de plataforma
	Type       string // "payment" | "internal_ledger"
	PeriodFrom time.Time
	PeriodTo   time.Time
}

type StartReconciliationResult struct {
	RunID string
}

// ProcessReportCmd procesa el informe del PSP para un run existente.
// Solo aplica a runs de tipo "payment".
type ProcessReportCmd struct {
	RunID      string
	ReportData []byte // CSV del PSP
}

// ProcessInternalCmd ejecuta la reconciliación interna del Ledger.
// Solo aplica a runs de tipo "internal_ledger".
type ProcessInternalCmd struct {
	RunID string
}

// ResolveDiscrepancyCmd resuelve o ignora una discrepancia.
type ResolveDiscrepancyCmd struct {
	DiscrepancyID string
	Action        string // "resolve" | "ignore"
	ResolvedBy    string // userID del operador
	Notes         string
}

// GetReportQuery solicita el reporte completo de un run.
type GetReportQuery struct {
	RunID         string
	StatusFilter  string // "" | "open" | "resolved" | "ignored"
	Limit         int
}

// ── Views ─────────────────────────────────────────────────────────────────────

type RunView struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id,omitempty"`
	Type             string     `json:"type"`
	Status           string     `json:"status"`
	PeriodFrom       time.Time  `json:"period_from"`
	PeriodTo         time.Time  `json:"period_to"`
	TotalRecords     int        `json:"total_records"`
	MatchedCount     int        `json:"matched_count"`
	DiscrepancyCount int        `json:"discrepancy_count"`
	ErrorMsg         string     `json:"error_msg,omitempty"`
	CreatedAt        string     `json:"created_at"`
	CompletedAt      *string    `json:"completed_at,omitempty"`
}

type ReportView struct {
	Run           RunView            `json:"run"`
	Discrepancies []DiscrepancyView  `json:"discrepancies"`
}

type DiscrepancyView struct {
	ID            string  `json:"id"`
	RunID         string  `json:"run_id"`
	Type          string  `json:"type"`
	RecordID      string  `json:"record_id"`
	SystemValue   string  `json:"system_value"`
	ExternalValue string  `json:"external_value,omitempty"`
	Status        string  `json:"status"`
	Notes         string  `json:"notes,omitempty"`
	ResolvedBy    string  `json:"resolved_by,omitempty"`
	CreatedAt     string  `json:"created_at"`
}
