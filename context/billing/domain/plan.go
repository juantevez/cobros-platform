package domain

import (
	"fmt"
	"time"
)

// PricingPlan es el agregado raíz del contexto Billing & Fees.
//
// Define las tarifas que se cobran por usar la plataforma:
//   - Una comisión por transacción (porcentaje en bps + monto fijo)
//   - Overrides por método de pago (tarjeta puede tener tasa distinta que billetera)
//   - Una suscripción mensual opcional
//
// La comisión se calcula como:
//
//	fee = ceil(amount × rate_bps / 10_000) + fixed_amount
//
// Usando basis points (1 bps = 0.01%) para evitar aritmética de punto flotante.
// Ejemplo: amount=$10.000 (1.000.000 centavos), rate=250 bps, fixed=50 centavos:
//
//	fee = ceil(1.000.000 × 250 / 10.000) + 50 = 25.000 + 50 = 25.050 centavos = $250.50
type PricingPlan struct {
	id          PlanID
	name        string
	description string
	// Tarifa base para cualquier método de pago.
	baseRateBps     int64  // 0–10000; 250 = 2.50%
	baseFixedAmount int64  // centavos; ≥ 0
	// Overrides por método de pago. Si existe, reemplaza la tarifa base.
	methodRates map[PaymentMethod]MethodRate
	// Cargo mensual fijo (0 = sin suscripción).
	monthlyFee int64
	currency   string
	active     bool
	createdAt  time.Time
	updatedAt  time.Time

	events []Event
}

// NewPricingPlan crea un plan de tarifas activo.
func NewPricingPlan(
	id PlanID,
	name, description string,
	baseRateBps, baseFixedAmount int64,
	monthlyFee int64,
	currency string,
) (*PricingPlan, error) {
	if name == "" {
		return nil, ErrPlanNameEmpty
	}
	if baseRateBps < 0 || baseRateBps > 10000 {
		return nil, ErrInvalidRateBps
	}
	if baseFixedAmount < 0 {
		return nil, ErrInvalidFixedAmount
	}
	if monthlyFee < 0 {
		return nil, ErrInvalidMonthlyFee
	}
	if _, err := NewMoney(0, currency); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	p := &PricingPlan{
		id:              id,
		name:            name,
		description:     description,
		baseRateBps:     baseRateBps,
		baseFixedAmount: baseFixedAmount,
		methodRates:     make(map[PaymentMethod]MethodRate),
		monthlyFee:      monthlyFee,
		currency:        currency,
		active:          true,
		createdAt:       now,
		updatedAt:       now,
	}

	p.record(PlanCreatedEvent{
		baseEvent: newBase(""), // evento de plataforma, sin tenant
		PlanID:    id.String(),
		PlanName:  name,
		RateBps:   baseRateBps,
	})

	return p, nil
}

// ReconstitutePricingPlan reconstruye el plan desde el repositorio.
func ReconstitutePricingPlan(
	id PlanID, name, description string,
	baseRateBps, baseFixedAmount, monthlyFee int64,
	methodRates map[PaymentMethod]MethodRate,
	currency string, active bool,
	createdAt, updatedAt time.Time,
) *PricingPlan {
	if methodRates == nil {
		methodRates = make(map[PaymentMethod]MethodRate)
	}
	return &PricingPlan{
		id: id, name: name, description: description,
		baseRateBps: baseRateBps, baseFixedAmount: baseFixedAmount,
		monthlyFee: monthlyFee, methodRates: methodRates,
		currency: currency, active: active,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// AddMethodRate agrega o reemplaza la tarifa para un método de pago específico.
func (p *PricingPlan) AddMethodRate(method PaymentMethod, rateBps, fixedAmount int64) error {
	if rateBps < 0 || rateBps > 10000 {
		return ErrInvalidRateBps
	}
	if fixedAmount < 0 {
		return ErrInvalidFixedAmount
	}
	p.methodRates[method] = MethodRate{RateBps: rateBps, FixedAmount: fixedAmount}
	p.updatedAt = time.Now().UTC()
	return nil
}

// Deactivate desactiva el plan. No puede usarse para nuevas asignaciones.
func (p *PricingPlan) Deactivate() {
	p.active = false
	p.updatedAt = time.Now().UTC()
}

// ── Cálculo de comisión ───────────────────────────────────────────────────────

// CalculateFee calcula la comisión para un pago dado.
//
// Usa el override de método de pago si existe; si no, la tarifa base.
// El redondeo es hacia arriba (ceil) para que nunca se cobre menos del mínimo.
//
// Fórmula:
//
//	rate_amount = ceil(amount × rate_bps / 10_000)
//	fee         = rate_amount + fixed_amount
func (p *PricingPlan) CalculateFee(amount int64, currency string, method PaymentMethod) (FeeBreakdown, error) {
	if amount <= 0 {
		return FeeBreakdown{}, fmt.Errorf("amount must be positive")
	}
	if currency != p.currency {
		return FeeBreakdown{}, fmt.Errorf("currency mismatch: plan is %q, payment is %q", p.currency, currency)
	}

	// Seleccionar tarifa: override por método si existe, base si no.
	rateBps := p.baseRateBps
	fixedAmount := p.baseFixedAmount
	methodOverride := false

	if override, ok := p.methodRates[method]; ok {
		rateBps = override.RateBps
		fixedAmount = override.FixedAmount
		methodOverride = true
	}

	// Cálculo con ceil para evitar redondeo en favor del comercio.
	// ceil(amount × rateBps / 10000) = (amount × rateBps + 9999) / 10000
	rateAmount := int64(0)
	if rateBps > 0 {
		rateAmount = (amount*rateBps + 9999) / 10000
	}

	totalFeeAmount := rateAmount + fixedAmount
	if totalFeeAmount < 0 {
		return FeeBreakdown{}, ErrFeeCalculationInvalid
	}

	return FeeBreakdown{
		RateBpsApplied: rateBps,
		RateAmount:     rateAmount,
		FixedAmount:    fixedAmount,
		TotalFee:       ReconstituteMoney(totalFeeAmount, currency),
		PlanID:         p.id.String(),
		MethodOverride: methodOverride,
	}, nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (p *PricingPlan) ID() PlanID                          { return p.id }
func (p *PricingPlan) Name() string                        { return p.name }
func (p *PricingPlan) Description() string                 { return p.description }
func (p *PricingPlan) BaseRateBps() int64                  { return p.baseRateBps }
func (p *PricingPlan) BaseFixedAmount() int64              { return p.baseFixedAmount }
func (p *PricingPlan) MonthlyFee() int64                   { return p.monthlyFee }
func (p *PricingPlan) Currency() string                    { return p.currency }
func (p *PricingPlan) Active() bool                        { return p.active }
func (p *PricingPlan) MethodRates() map[PaymentMethod]MethodRate { return p.methodRates }
func (p *PricingPlan) CreatedAt() time.Time                { return p.createdAt }
func (p *PricingPlan) UpdatedAt() time.Time                { return p.updatedAt }

func (p *PricingPlan) PullEvents() []Event {
	evs := p.events
	p.events = nil
	return evs
}

func (p *PricingPlan) record(e Event) { p.events = append(p.events, e) }
