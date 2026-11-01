package application

import "time"

// InitiatePayoutCmd solicita un desembolso para un tenant.
type InitiatePayoutCmd struct {
	TenantID       string
	Amount         int64 // si es 0, usa el saldo disponible completo
	Currency       string
	IdempotencyKey string // obligatorio: previene desembolsos duplicados por retries
}

// InitiatePayoutResult es también la respuesta HTTP: snake_case, como
// PayoutView (antes salía en PascalCase).
type InitiatePayoutResult struct {
	PayoutID      string `json:"payout_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
	BankReference string `json:"bank_reference,omitempty"`
	Replayed      bool   `json:"replayed"` // true si se retornó un payout existente (retry idempotente)
}

// GetPayoutQuery consulta el estado de un payout.
type GetPayoutQuery struct {
	TenantID string
	PayoutID string
}

// ListPayoutsQuery lista los payouts de un tenant.
type ListPayoutsQuery struct {
	TenantID string
	Limit    int
}

// PayoutView es la representación de lectura de un payout.
type PayoutView struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	Amount          int64      `json:"amount"`
	Currency        string     `json:"currency"`
	Status          string     `json:"status"`
	BankAccountType string     `json:"bank_account_type"`
	BankAccountNum  string     `json:"bank_account_number"`
	HolderName      string     `json:"holder_name"`
	BankReference   string     `json:"bank_reference,omitempty"`
	FailureReason   string     `json:"failure_reason,omitempty"`
	InitiatedAt     *time.Time `json:"initiated_at,omitempty"`
	ConfirmedAt     *time.Time `json:"confirmed_at,omitempty"`
	FailedAt        *time.Time `json:"failed_at,omitempty"`
	CreatedAt       string     `json:"created_at"`
}
