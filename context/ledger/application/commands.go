package application

import "time"

// ── Account ───────────────────────────────────────────────────────────────────

type CreateAccountCmd struct {
	TenantID    string
	AccountType string // "merchant_balance" | "platform_fees" | "reserve" | ...
	Currency    string // ISO 4217: "ARS", "USD"
	Description string
}

type CreateAccountResult struct {
	AccountID string
}

type GetBalanceQuery struct {
	TenantID  string
	AccountID string
}

type GetBalanceResult struct {
	AccountID string
	Balance   int64  // en centavos; puede ser negativo en cuentas de pasivo
	Currency  string
}

// ── JournalEntry ─────────────────────────────────────────────────────────────

type PostEntryCmd struct {
	TenantID string
	// IdempotencyKey identifica unívocamente este asiento en el tenant.
	// El caller (ej: PaymentProcessing) lo genera desde el ID de la operación.
	IdempotencyKey string
	Description    string
	OccurredAt     time.Time         // cuándo ocurrió el hecho económico
	Metadata       map[string]string // datos de contexto para trazabilidad
	Lines          []PostingLine
}

type PostingLine struct {
	AccountID string
	Direction string // "debit" | "credit"
	Amount    int64  // en centavos, > 0
	Currency  string
}

type PostEntryResult struct {
	EntryID     string
	CreatedAt   time.Time
	WasExisting bool // true si la clave de idempotencia ya existía (reintento)
}

type ReverseEntryCmd struct {
	TenantID        string
	OriginalEntryID string
}

type ReverseEntryResult struct {
	ReverseEntryID string
}
