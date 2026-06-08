package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type AccountID string
type EntryID string
type PostingID string
type TenantID string

func NewAccountID() AccountID { return AccountID(uuid.NewString()) }
func NewEntryID() EntryID     { return EntryID(uuid.NewString()) }
func NewPostingID() PostingID { return PostingID(uuid.NewString()) }

func ParseAccountID(s string) (AccountID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid account id: %w", err)
	}
	return AccountID(s), nil
}

func ParseEntryID(s string) (EntryID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid entry id: %w", err)
	}
	return EntryID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id AccountID) String() string { return string(id) }
func (id EntryID) String() string   { return string(id) }
func (id PostingID) String() string { return string(id) }
func (id TenantID) String() string  { return string(id) }

// ── AccountType ───────────────────────────────────────────────────────────────

// AccountType clasifica la naturaleza contable de una cuenta.
// Determina cómo interpretar el saldo (positivo/negativo).
type AccountType string

const (
	// MerchantBalance: pasivo de la plataforma hacia el comercio.
	// Saldo positivo = la plataforma le debe ese monto al comercio.
	AccountTypeMerchantBalance AccountType = "merchant_balance"

	// PlatformFees: ingresos de la plataforma por comisiones.
	// Saldo positivo = la plataforma ganó ese monto.
	AccountTypePlatformFees AccountType = "platform_fees"

	// Reserve: fondos del comercio retenidos temporalmente (rolling reserve).
	// Se liberan cuando el período de retención vence.
	AccountTypeReserve AccountType = "reserve"

	// InTransit: fondos capturados del pagador pero aún no liquidados por el PSP.
	AccountTypeInTransit AccountType = "in_transit"

	// DisputeHold: fondos congelados mientras hay una disputa abierta.
	AccountTypeDisputeHold AccountType = "dispute_hold"
)

func ParseAccountType(s string) (AccountType, error) {
	t := AccountType(s)
	switch t {
	case AccountTypeMerchantBalance, AccountTypePlatformFees,
		AccountTypeReserve, AccountTypeInTransit, AccountTypeDisputeHold:
		return t, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidAccountType, s)
}

func (t AccountType) String() string { return string(t) }

// ── Direction ─────────────────────────────────────────────────────────────────

// Direction indica si un posting es un débito o un crédito.
type Direction string

const (
	DirectionDebit  Direction = "debit"
	DirectionCredit Direction = "credit"
)

func ParseDirection(s string) (Direction, error) {
	d := Direction(s)
	switch d {
	case DirectionDebit, DirectionCredit:
		return d, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidDirection, s)
}

func (d Direction) String() string { return string(d) }

// Opposite retorna la dirección opuesta. Usado al construir asientos de reversa.
func (d Direction) Opposite() Direction {
	if d == DirectionDebit {
		return DirectionCredit
	}
	return DirectionDebit
}

// ── Money ─────────────────────────────────────────────────────────────────────

// Money representa un monto monetario en unidades mínimas (centavos).
//
// Invariantes:
//   - Amount es siempre >= 0. Los débitos y créditos se expresan con Direction.
//   - Currency es un código ISO 4217 de 3 letras en mayúsculas.
//   - NUNCA usar punto flotante para montos monetarios.
type Money struct {
	amount   int64  // en unidades mínimas (centavos, satoshis, etc.)
	currency string // ISO 4217: "ARS", "USD", "EUR"
}

// NewMoney crea un Money validando amount y currency.
func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, ErrNegativeAmount
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{amount: amount, currency: cur}, nil
}

// MustMoney crea un Money o entra en pánico. Solo para tests y constantes internas.
func MustMoney(amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(fmt.Sprintf("MustMoney: %v", err))
	}
	return m
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.amount == 0 }

// Add suma dos Money de la misma moneda.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrInvalidMoneyOp
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// Equal compara dos Money.
func (m Money) Equal(other Money) bool {
	return m.amount == other.amount && m.currency == other.currency
}

func (m Money) String() string {
	return fmt.Sprintf("%d %s", m.amount, m.currency)
}
