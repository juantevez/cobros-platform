package application

import "time"

// ── Submit ────────────────────────────────────────────────────────────────────

type SubmitApplicationCmd struct {
	TenantID         string
	LegalName        string
	TaxID            string
	BusinessCategory string
	// Dirección
	Street     string
	City       string
	State      string
	Country    string
	PostalCode string
	// Contacto
	Website     string
	PhoneNumber string
}

type SubmitApplicationResult struct {
	ApplicationID string
}

// ── Document ──────────────────────────────────────────────────────────────────

type UploadDocumentCmd struct {
	TenantID     string
	DocumentType string
	Reference    string // URL o ID externo del archivo almacenado
}

// ── Person ────────────────────────────────────────────────────────────────────

type AddPersonCmd struct {
	TenantID          string
	FullName          string
	Role              string // "owner" | "director" | "ubo"
	IdentityDocType   string
	IdentityDocNumber string
	Nationality       string
}

// ── Bank Account ──────────────────────────────────────────────────────────────

type SetBankAccountCmd struct {
	TenantID      string
	AccountType   string // "CBU" | "CVU" | "IBAN"
	AccountNumber string
	BankName      string
	HolderName    string
	Currency      string // "ARS" | "USD"
}

// ── Submit for Review ─────────────────────────────────────────────────────────

type SubmitForReviewCmd struct {
	TenantID string
}

// ── Review (operador) ─────────────────────────────────────────────────────────

type ReviewApplicationCmd struct {
	ApplicationID string
	Decision      string // "approve" | "reject" | "request_more_info"
	Notes         string // requerido para reject y request_more_info
}

// ── Get ───────────────────────────────────────────────────────────────────────

type GetApplicationQuery struct {
	TenantID string // usado por el comercio para ver su propia solicitud
}

// ApplicationView es la representación de lectura de una solicitud.
type ApplicationView struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenant_id"`
	Status           string            `json:"status"`
	LegalName        string            `json:"legal_name"`
	TaxID            string            `json:"tax_id"`
	BusinessCategory string            `json:"business_category"`
	DocumentCount    int               `json:"document_count"`
	PersonCount      int               `json:"person_count"`
	HasBankAccount   bool              `json:"has_bank_account"`
	ReviewNotes      string            `json:"review_notes,omitempty"`
	RejectionReason  string            `json:"rejection_reason,omitempty"`
	SubmittedAt      *time.Time        `json:"submitted_at,omitempty"`
	ReviewedAt       *time.Time        `json:"reviewed_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
