package application

import "time"

// InitiatePayoutCmd solicita un desembolso para un tenant.
type InitiatePayoutCmd struct {
	TenantID string
	Amount   int64  // si es 0, usa el saldo disponible completo
	Currency string
}

type InitiatePayoutResult struct {
	PayoutID      string
	Amount        int64
	Currency      string
	Status        string
	BankReference string
}

// ConfirmPayoutCmd registra la confirmación del banco.
type ConfirmPayoutCmd struct {
	TenantID      string
	PayoutID      string
	BankReference string
}

// FailPayoutCmd registra el fallo de una transferencia.
type FailPayoutCmd struct {
	TenantID      string
	PayoutID      string
	FailureReason string
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
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	Status         string     `json:"status"`
	BankAccountType string    `json:"bank_account_type"`
	BankAccountNum string     `json:"bank_account_number"`
	HolderName     string     `json:"holder_name"`
	BankReference  string     `json:"bank_reference,omitempty"`
	FailureReason  string     `json:"failure_reason,omitempty"`
	InitiatedAt    *time.Time `json:"initiated_at,omitempty"`
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	CreatedAt      string     `json:"created_at"`
}
