package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type PlanID string
type TenantPlanID string
type FeeID string
type TenantID string

func NewPlanID() PlanID           { return PlanID(uuid.NewString()) }
func NewTenantPlanID() TenantPlanID { return TenantPlanID(uuid.NewString()) }
func NewFeeID() FeeID             { return FeeID(uuid.NewString()) }

func ParsePlanID(s string) (PlanID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid plan id: %w", err)
	}
	return PlanID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id PlanID) String() string       { return string(id) }
func (id TenantPlanID) String() string { return string(id) }
func (id FeeID) String() string        { return string(id) }
func (id TenantID) String() string     { return string(id) }

// ── PaymentMethod ─────────────────────────────────────────────────────────────

// PaymentMethod replica los métodos de pago del contexto Payment.
// No importamos el tipo de Payment para mantener el aislamiento entre contextos.
type PaymentMethod string

const (
	MethodCard     PaymentMethod = "card"
	MethodWallet   PaymentMethod = "wallet"
	MethodTransfer PaymentMethod = "transfer"
	MethodQR       PaymentMethod = "qr"
)

func ParsePaymentMethod(s string) (PaymentMethod, error) {
	m := PaymentMethod(s)
	switch m {
	case MethodCard, MethodWallet, MethodTransfer, MethodQR:
		return m, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidPaymentMethod, s)
}

func (m PaymentMethod) String() string { return string(m) }

// ── Money ─────────────────────────────────────────────────────────────────────

// Money representa un monto en unidades mínimas (centavos). Nunca float.
type Money struct {
	amount   int64
	currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, ErrInvalidFixedAmount
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{amount: amount, currency: cur}, nil
}

func ZeroMoney(currency string) Money {
	return Money{amount: 0, currency: strings.ToUpper(currency)}
}

func ReconstituteMoney(amount int64, currency string) Money {
	return Money{amount: amount, currency: currency}
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.amount == 0 }
func (m Money) String() string   { return fmt.Sprintf("%d %s", m.amount, m.currency) }

// ── MethodRate ────────────────────────────────────────────────────────────────

// MethodRate define una tarifa específica para un método de pago.
// Si un plan tiene MethodRate para "card", usa esa tarifa en lugar de la base.
type MethodRate struct {
	RateBps     int64 // tasa en basis points (1 bps = 0.01%). Rango: 0–10000
	FixedAmount int64 // monto fijo adicional en centavos (≥ 0)
}

// ── FeeBreakdown ──────────────────────────────────────────────────────────────

// FeeBreakdown desglosa cómo se calculó una comisión.
// Útil para mostrar al comercio el detalle de cada cobro.
type FeeBreakdown struct {
	RateBpsApplied  int64  // tasa usada en basis points
	RateAmount      int64  // parte proporcional de la comisión (centavos)
	FixedAmount     int64  // parte fija de la comisión (centavos)
	TotalFee        Money  // suma total
	PlanID          string
	MethodOverride  bool   // true si se usó un override por método de pago
}
