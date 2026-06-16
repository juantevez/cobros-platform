package domain

import (
	"fmt"
	"time"
)

// TenantPlan representa la asignación de un PricingPlan a un Tenant específico.
//
// Permite que cada comercio tenga tarifas negociadas individualmente:
// un override de rate_bps o fixed_amount prevalece sobre el plan base.
// Si no hay override, se usan las tarifas del plan tal cual.
//
// Solo puede haber un TenantPlan activo por tenant a la vez.
// Al asignar uno nuevo, el anterior debe desactivarse primero.
type TenantPlan struct {
	id       TenantPlanID
	tenantID TenantID
	planID   PlanID
	planName string // snapshot del nombre al momento de asignar

	// Overrides negociados (nil = usar los del plan base).
	customRateBps     *int64
	customFixedAmount *int64

	active    bool
	validFrom time.Time
	validUntil *time.Time // nil = vigente indefinidamente
	createdAt time.Time

	events []Event
}

// NewTenantPlan asigna un plan a un tenant.
// customRateBps y customFixedAmount son opcionales (-1 = usar los del plan).
func NewTenantPlan(
	id TenantPlanID,
	tenantID TenantID,
	plan *PricingPlan,
	customRateBps, customFixedAmount int64, // -1 = sin override
	validFrom time.Time,
) (*TenantPlan, error) {
	if !plan.Active() {
		return nil, ErrPlanInactive
	}

	tp := &TenantPlan{
		id:        id,
		tenantID:  tenantID,
		planID:    plan.ID(),
		planName:  plan.Name(),
		active:    true,
		validFrom: validFrom,
		createdAt: time.Now().UTC(),
	}

	if customRateBps >= 0 {
		if customRateBps > 10000 {
			return nil, ErrInvalidRateBps
		}
		v := customRateBps
		tp.customRateBps = &v
	}
	if customFixedAmount >= 0 {
		v := customFixedAmount
		tp.customFixedAmount = &v
	}

	tp.record(PlanAssignedEvent{
		baseEvent:    newBase(tenantID.String()),
		TenantPlanID: id.String(),
		TenantID:     tenantID.String(),
		PlanID:       plan.ID().String(),
		PlanName:     plan.Name(),
	})

	return tp, nil
}

// ReconstituteTenantPlan reconstruye desde el repositorio.
func ReconstituteTenantPlan(
	id TenantPlanID, tenantID TenantID, planID PlanID, planName string,
	customRateBps, customFixedAmount *int64,
	active bool, validFrom time.Time, validUntil *time.Time, createdAt time.Time,
) *TenantPlan {
	return &TenantPlan{
		id: id, tenantID: tenantID, planID: planID, planName: planName,
		customRateBps: customRateBps, customFixedAmount: customFixedAmount,
		active: active, validFrom: validFrom, validUntil: validUntil, createdAt: createdAt,
	}
}

// Deactivate desactiva la asignación. Llamar antes de asignar un plan nuevo.
func (tp *TenantPlan) Deactivate() {
	tp.active = false
	now := time.Now().UTC()
	tp.validUntil = &now
}

// CalculateFee calcula la comisión aplicando los overrides del tenant si existen.
// Siempre llama a plan.CalculateFee y luego aplica los overrides.
func (tp *TenantPlan) CalculateFee(plan *PricingPlan, amount int64, currency string, method PaymentMethod) (FeeBreakdown, error) {
	if plan.ID() != tp.planID {
		return FeeBreakdown{}, fmt.Errorf("plan mismatch: tenant plan references %s but got %s", tp.planID, plan.ID())
	}

	breakdown, err := plan.CalculateFee(amount, currency, method)
	if err != nil {
		return FeeBreakdown{}, err
	}

	// Aplicar overrides del tenant si existen.
	// Los overrides reemplazan solo la parte que personalizan.
	if tp.customRateBps != nil || tp.customFixedAmount != nil {
		rateBps := breakdown.RateBpsApplied
		fixedAmount := breakdown.FixedAmount

		if tp.customRateBps != nil {
			rateBps = *tp.customRateBps
		}
		if tp.customFixedAmount != nil {
			fixedAmount = *tp.customFixedAmount
		}

		rateAmount := int64(0)
		if rateBps > 0 {
			rateAmount = (amount*rateBps + 9999) / 10000
		}
		totalFee := rateAmount + fixedAmount

		breakdown = FeeBreakdown{
			RateBpsApplied: rateBps,
			RateAmount:     rateAmount,
			FixedAmount:    fixedAmount,
			TotalFee:       ReconstituteMoney(totalFee, currency),
			PlanID:         tp.planID.String(),
			MethodOverride: false, // el override de tenant prevalece sobre el de método
		}
	}

	return breakdown, nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (tp *TenantPlan) ID() TenantPlanID         { return tp.id }
func (tp *TenantPlan) TenantID() TenantID        { return tp.tenantID }
func (tp *TenantPlan) PlanID() PlanID            { return tp.planID }
func (tp *TenantPlan) PlanName() string          { return tp.planName }
func (tp *TenantPlan) CustomRateBps() *int64     { return tp.customRateBps }
func (tp *TenantPlan) CustomFixedAmount() *int64 { return tp.customFixedAmount }
func (tp *TenantPlan) Active() bool              { return tp.active }
func (tp *TenantPlan) ValidFrom() time.Time      { return tp.validFrom }
func (tp *TenantPlan) ValidUntil() *time.Time    { return tp.validUntil }
func (tp *TenantPlan) CreatedAt() time.Time      { return tp.createdAt }

func (tp *TenantPlan) PullEvents() []Event {
	evs := tp.events
	tp.events = nil
	return evs
}

func (tp *TenantPlan) record(e Event) { tp.events = append(tp.events, e) }
