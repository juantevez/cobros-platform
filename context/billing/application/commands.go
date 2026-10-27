package application

import "time"

// ── CreatePlan ────────────────────────────────────────────────────────────────

type CreatePlanCmd struct {
	Name            string
	Description     string
	BaseRateBps     int64  // tasa base en basis points (250 = 2.50%)
	BaseFixedAmount int64  // monto fijo base en centavos
	MonthlyFee      int64  // cargo mensual en centavos (0 = sin suscripción)
	Currency        string // ISO 4217
	// MethodRates son overrides opcionales por método de pago.
	MethodRates []MethodRateInput
}

type MethodRateInput struct {
	Method      string // "card" | "wallet" | "transfer" | "qr"
	RateBps     int64
	FixedAmount int64
}

type CreatePlanResult struct {
	PlanID string `json:"plan_id"`
}

// ── AssignPlan ────────────────────────────────────────────────────────────────

type AssignPlanCmd struct {
	TenantID string
	PlanID   string
	// Overrides negociados para este tenant específico.
	// Usar -1 para indicar "sin override, usar el del plan base".
	CustomRateBps     int64
	CustomFixedAmount int64
	ValidFrom         time.Time // si es zero, usa time.Now()
}

type AssignPlanResult struct {
	TenantPlanID string `json:"tenant_plan_id"`
	PlanName     string `json:"plan_name"`
}

// ── CalculateFee ──────────────────────────────────────────────────────────────

// CalculateFeeQuery es la consulta de comisión para un pago.
// Este es el contrato que el contexto Payment usará para obtener la fee real.
type CalculateFeeQuery struct {
	TenantID      string
	Amount        int64 // en centavos
	Currency      string
	PaymentMethod string // "card" | "wallet" | "transfer" | "qr"
}

type CalculateFeeResult struct {
	FeeAmount      int64 // comisión total en centavos
	Currency       string
	RateBpsApplied int64 // tasa usada (para auditoría)
	RateAmount     int64 // parte proporcional de la comisión
	FixedAmount    int64 // parte fija de la comisión
	PlanID         string
	PlanName       string
	MethodOverride bool // true si se usó override por método de pago
	TenantOverride bool // true si el tenant tiene tarifas negociadas
	// CurrencyMismatch: moneda del pago ≠ moneda del plan → solo bps, sin fijos.
	CurrencyMismatch bool
	// Fallback: el tenant no tiene plan asignado → se aplicó FallbackRateBps.
	Fallback bool
}

// ── GetPlan / ListPlans ───────────────────────────────────────────────────────

type GetPlanQuery struct {
	PlanID string
}

type PlanView struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	BaseRateBps     int64            `json:"base_rate_bps"`
	BaseRatePercent string           `json:"base_rate_percent"` // "2.50%"
	BaseFixedAmount int64            `json:"base_fixed_amount"`
	MonthlyFee      int64            `json:"monthly_fee"`
	Currency        string           `json:"currency"`
	Active          bool             `json:"active"`
	MethodRates     []MethodRateView `json:"method_rates,omitempty"`
	CreatedAt       string           `json:"created_at"`
}

type MethodRateView struct {
	Method      string `json:"method"`
	RateBps     int64  `json:"rate_bps"`
	RatePercent string `json:"rate_percent"`
	FixedAmount int64  `json:"fixed_amount"`
}

type GetTenantPlanQuery struct {
	TenantID string
}

// ListPlansPageQuery pagina el catálogo de planes.
type ListPlansPageQuery struct {
	Limit           int // 0 → default 20; máx 100
	Offset          int
	IncludeInactive bool // ?include_inactive=true (gestión)
}

// ── UpdatePlan / DeactivatePlan ───────────────────────────────────────────────

// UpdatePlanCmd aplica un PATCH sobre el plan: los campos puntero nil no se tocan.
// MethodRates: nil = no tocar los overrides; slice (aunque vacío) = reemplazo
// completo del conjunto de overrides por método.
type UpdatePlanCmd struct {
	PlanID          string
	BaseRateBps     *int64
	BaseFixedAmount *int64
	MonthlyFee      *int64
	MethodRates     []MethodRateInput
}

type DeactivatePlanCmd struct {
	PlanID string
}

// ── ListTenantPlans (historial de asignaciones) ───────────────────────────────

type ListTenantPlansQuery struct {
	TenantID string
	Limit    int // 0 → default 20; máx 100
	Offset   int
}

type TenantPlanView struct {
	ID                string  `json:"id,omitempty"`
	TenantID          string  `json:"tenant_id"`
	PlanID            string  `json:"plan_id,omitempty"`
	PlanName          string  `json:"plan_name"`
	CustomRateBps     *int64  `json:"custom_rate_bps,omitempty"`
	CustomFixedAmount *int64  `json:"custom_fixed_amount,omitempty"`
	Active            bool    `json:"active"`
	ValidFrom         string  `json:"valid_from,omitempty"`
	ValidUntil        *string `json:"valid_until,omitempty"`
	// Fallback es true cuando el tenant no tiene plan asignado: se le cobra
	// FallbackRateBps (GET /my-plan lo muestra en vez de responder 404).
	Fallback        bool   `json:"fallback,omitempty"`
	FallbackRateBps *int64 `json:"fallback_rate_bps,omitempty"`
}
