package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type PaymentID string
type TenantID string

func NewPaymentID() PaymentID { return PaymentID(uuid.NewString()) }

func ParsePaymentID(s string) (PaymentID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid payment id: %w", err)
	}
	return PaymentID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id PaymentID) String() string { return string(id) }
func (id TenantID) String() string  { return string(id) }

// ── PaymentStatus ─────────────────────────────────────────────────────────────

type PaymentStatus string

const (
	StatusInitiated    PaymentStatus = "initiated"     // creado, esperando procesamiento
	StatusProcessing   PaymentStatus = "processing"    // enviado al PSP
	StatusCaptured     PaymentStatus = "captured"      // fondos capturados exitosamente
	StatusRiskRejected PaymentStatus = "risk_rejected" // rechazado por evaluación de riesgo
	StatusFailed       PaymentStatus = "failed"        // rechazado por el PSP
	StatusRefunded     PaymentStatus = "refunded"      // devuelto al pagador
)

func (s PaymentStatus) String() string  { return string(s) }
func (s PaymentStatus) IsFinal() bool {
	return s == StatusCaptured || s == StatusRiskRejected ||
		s == StatusFailed || s == StatusRefunded
}

// ── PaymentMethod ─────────────────────────────────────────────────────────────

type PaymentMethod string

const (
	MethodCard     PaymentMethod = "card"     // tarjeta crédito/débito
	MethodWallet   PaymentMethod = "wallet"   // billetera (MP, PayPal)
	MethodTransfer PaymentMethod = "transfer" // transferencia bancaria
	MethodQR       PaymentMethod = "qr"       // QR (puede ser wallet o transfer)
)

func ParsePaymentMethod(s string) (PaymentMethod, error) {
	m := PaymentMethod(s)
	switch m {
	case MethodCard, MethodWallet, MethodTransfer, MethodQR:
		return m, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMethod, s)
}

func (m PaymentMethod) String() string { return string(m) }

// ── Money ─────────────────────────────────────────────────────────────────────

// Money representa un monto en unidades mínimas (centavos). Nunca float.
type Money struct {
	amount   int64
	currency string // ISO 4217
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount <= 0 {
		return Money{}, ErrInvalidAmount
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{amount: amount, currency: cur}, nil
}

func ReconstituteMoney(amount int64, currency string) Money {
	return Money{amount: amount, currency: currency}
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.amount == 0 }

func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("cannot subtract money with different currencies")
	}
	if other.amount > m.amount {
		return Money{}, ErrRefundExceedsAmount
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}

func (m Money) String() string { return fmt.Sprintf("%d %s", m.amount, m.currency) }

// ── PayerInfo ─────────────────────────────────────────────────────────────────

// PayerInfo contiene los datos identificatorios del pagador.
// NUNCA incluye datos de tarjeta en crudo — eso se tokeniza en el PSP.
type PayerInfo struct {
	Name    string
	Email   string
	DocType string // "DNI", "CUIT", etc.
	DocNum  string
}
