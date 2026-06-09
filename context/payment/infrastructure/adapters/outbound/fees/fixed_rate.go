// Package fees provee calculadores de comisiones para el contexto Payment.
package fees

import (
	"fmt"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// FixedRateCalculator calcula una comisión como porcentaje fijo del monto.
// En Fase 3 se reemplaza por un calculador que consulta el plan del tenant
// en el módulo Billing & Fees.
type FixedRateCalculator struct {
	// RateBps es la tasa en puntos base (1 bps = 0.01%).
	// Ejemplo: 300 bps = 3.00%.
	RateBps int64
}

// NewFixedRateCalculator crea un calculador con la tasa dada en puntos base.
// Default razonable: 300 bps (3%).
func NewFixedRateCalculator(rateBps int64) *FixedRateCalculator {
	return &FixedRateCalculator{RateBps: rateBps}
}

func (c *FixedRateCalculator) Calculate(
	_ domain.TenantID,
	amount domain.Money,
	_ domain.PaymentMethod,
) (domain.Money, error) {
	if c.RateBps <= 0 {
		return domain.ReconstituteMoney(0, amount.Currency()), nil
	}

	// fee = amount * rateBps / 10000 (redondeo hacia arriba)
	fee := (amount.Amount()*c.RateBps + 9999) / 10000
	if fee < 1 {
		fee = 1
	}

	m := domain.ReconstituteMoney(fee, amount.Currency())
	if m.IsZero() {
		return domain.ReconstituteMoney(0, amount.Currency()), nil
	}

	result := domain.ReconstituteMoney(fee, amount.Currency())
	_ = fmt.Sprintf("fee calc: %d bps of %s = %d %s",
		c.RateBps, amount, fee, amount.Currency())

	return result, nil
}
