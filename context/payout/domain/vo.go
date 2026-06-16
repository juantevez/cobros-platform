package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type PayoutID string
type TenantID string

func NewPayoutID() PayoutID { return PayoutID(uuid.NewString()) }

func ParsePayoutID(s string) (PayoutID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid payout id: %w", err)
	}
	return PayoutID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id PayoutID) String() string { return string(id) }
func (id TenantID) String() string { return string(id) }

// ── PayoutStatus ──────────────────────────────────────────────────────────────

type PayoutStatus string

const (
	// StatusInitiated: calculado y registrado en Ledger. Listo para enviar.
	StatusInitiated PayoutStatus = "initiated"
	// StatusProcessing: transferencia enviada al banco, esperando confirmación.
	StatusProcessing PayoutStatus = "processing"
	// StatusConfirmed: el banco confirmó la acreditación. Estado final exitoso.
	StatusConfirmed PayoutStatus = "confirmed"
	// StatusFailed: la transferencia falló (cuenta inválida, rechazo bancario).
	// El asiento en Ledger se revierte automáticamente.
	StatusFailed PayoutStatus = "failed"
)

func (s PayoutStatus) String() string { return string(s) }
func (s PayoutStatus) IsFinal() bool {
	return s == StatusConfirmed || s == StatusFailed
}

// ── Money ─────────────────────────────────────────────────────────────────────

// Money representa un monto en unidades mínimas (centavos). Nunca float.
type Money struct {
	amount   int64
	currency string
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
func (m Money) String() string   { return fmt.Sprintf("%d %s", m.amount, m.currency) }

// ── BankAccountInfo ───────────────────────────────────────────────────────────

// BankAccountInfo contiene los datos de la cuenta bancaria destino del payout.
// Se obtiene del contexto Onboarding al momento de calcular el payout.
type BankAccountInfo struct {
	AccountType   string // "CBU", "CVU", "IBAN"
	AccountNumber string
	BankName      string
	HolderName    string
	Currency      string
}
