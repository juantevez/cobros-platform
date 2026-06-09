package application

import "time"

// ProcessPaymentCmd es el comando para procesar un pago.
type ProcessPaymentCmd struct {
	TenantID string
	// IdempotencyKey garantiza que el mismo pago no se procese dos veces.
	// El caller (ej: Checkout) lo genera desde el ID de la operación.
	IdempotencyKey string
	Amount         int64  // en centavos
	Currency       string // ISO 4217
	PaymentMethod  string // "card" | "wallet" | "transfer" | "qr"
	// PaymentToken es el token generado por el SDK del PSP en el frontend.
	// NUNCA enviar datos de tarjeta en crudo a este endpoint.
	PaymentToken string
	Description  string
	// PayerInfo datos del pagador
	PayerName    string
	PayerEmail   string
	PayerDocType string
	PayerDocNum  string
	// CheckoutID referencia al Checkout (Fase 3); vacío en Fase 2
	CheckoutID string
	Metadata   map[string]string
}

type ProcessPaymentResult struct {
	PaymentID    string
	Status       string
	PSPReference string
	Amount       int64
	Currency     string
	PlatformFee  int64
	CapturedAt   *time.Time
	WasExisting  bool // true si el pago ya existía (reintento idempotente)
}

// RefundPaymentCmd solicita el reembolso de un pago capturado.
type RefundPaymentCmd struct {
	TenantID  string
	PaymentID string
}

type RefundPaymentResult struct {
	PaymentID    string
	PSPReference string
	Status       string
}

// GetPaymentQuery consulta el estado de un pago.
type GetPaymentQuery struct {
	TenantID  string
	PaymentID string
}

type PaymentView struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	Status         string     `json:"status"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	PlatformFee    int64      `json:"platform_fee"`
	PSPFee         int64      `json:"psp_fee"`
	NetAmount      int64      `json:"net_amount"`
	PaymentMethod  string     `json:"payment_method"`
	PSPName        string     `json:"psp_name,omitempty"`
	PSPReference   string     `json:"psp_reference,omitempty"`
	FailureReason  string     `json:"failure_reason,omitempty"`
	IdempotencyKey string     `json:"idempotency_key"`
	CapturedAt     *time.Time `json:"captured_at,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	CreatedAt      string     `json:"created_at"`
}
