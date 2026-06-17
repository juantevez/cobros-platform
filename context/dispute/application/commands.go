package application

import "time"

// OpenDisputeCmd es disparado por el operador cuando el banco notifica una disputa.
type OpenDisputeCmd struct {
	TenantID     string
	PaymentID    string
	PSPReference string    // ID de la disputa en el PSP
	Amount       int64     // monto disputado en centavos
	Currency     string
	Reason       string    // "fraudulent" | "product_not_received" | etc.
	Deadline     time.Time // fecha límite para contestar
}

type OpenDisputeResult struct {
	DisputeID string
}

// ContestDisputeCmd envía evidencia del comercio para contestar la disputa.
type ContestDisputeCmd struct {
	TenantID   string
	DisputeID  string
	Evidence   []EvidenceInput
	Note       string
}

type EvidenceInput struct {
	EvidenceType string // "receipt" | "tracking" | "communication" | "other"
	Reference    string // URL o ID externo del documento
	Description  string
}

// AcceptDisputeCmd acepta la disputa sin contestar.
type AcceptDisputeCmd struct {
	TenantID  string
	DisputeID string
	Note      string
}

// ResolveDisputeCmd registra el resultado final del banco.
// Solo lo ejecuta el operador (platform_support).
type ResolveDisputeCmd struct {
	DisputeID string
	Outcome   string // "won" | "lost"
	Note      string
}

// GetDisputeQuery consulta una disputa.
type GetDisputeQuery struct {
	TenantID  string
	DisputeID string
}

// ListDisputesQuery lista disputas con filtros.
type ListDisputesQuery struct {
	TenantID     string
	StatusFilter string // "" | "open" | "under_review" | "won" | "lost" | ...
	Limit        int
}

// ── Views ─────────────────────────────────────────────────────────────────────

type DisputeView struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	PaymentID    string         `json:"payment_id"`
	PSPReference string         `json:"psp_reference,omitempty"`
	Amount       int64          `json:"amount"`
	Currency     string         `json:"currency"`
	Reason       string         `json:"reason"`
	Status       string         `json:"status"`
	Evidence     []EvidenceView `json:"evidence,omitempty"`
	ResponseNote string         `json:"response_note,omitempty"`
	ResolvedNote string         `json:"resolved_note,omitempty"`
	Deadline     string         `json:"deadline"`
	OpenedAt     string         `json:"opened_at"`
	RespondedAt  *string        `json:"responded_at,omitempty"`
	ResolvedAt   *string        `json:"resolved_at,omitempty"`
	IsOverdue    bool           `json:"is_overdue"`
}

type EvidenceView struct {
	ID           string `json:"id"`
	EvidenceType string `json:"evidence_type"`
	Reference    string `json:"reference"`
	Description  string `json:"description"`
	SubmittedAt  string `json:"submitted_at"`
}
