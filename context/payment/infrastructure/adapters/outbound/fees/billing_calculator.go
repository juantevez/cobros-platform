package fees

import (
	"context"
	"fmt"

	billingapp "github.com/juantevez/cobros-platform/context/billing/application"
	paymentdomain "github.com/juantevez/cobros-platform/context/payment/domain"
)

// BillingFeeCalculator implementa el puerto FeeCalculator de Payment
// consultando el plan real del tenant en el contexto Billing & Fees.
//
// Reemplaza al FixedRateCalculator en producción.
// Si el tenant no tiene plan asignado, Billing aplica el fallback interno.
type BillingFeeCalculator struct {
	calculateFee *billingapp.CalculateFeeUseCase
}

func NewBillingFeeCalculator(calculateFee *billingapp.CalculateFeeUseCase) *BillingFeeCalculator {
	return &BillingFeeCalculator{calculateFee: calculateFee}
}

func (c *BillingFeeCalculator) Calculate(
	ctx context.Context,
	tenantID paymentdomain.TenantID,
	amount paymentdomain.Money,
	method paymentdomain.PaymentMethod,
) (paymentdomain.Money, error) {
	result, err := c.calculateFee.Execute(ctx, billingapp.CalculateFeeQuery{
		TenantID:      tenantID.String(),
		Amount:        amount.Amount(),
		Currency:      amount.Currency(),
		PaymentMethod: method.String(),
	})
	if err != nil {
		return paymentdomain.Money{}, fmt.Errorf("billing fee calculator: %w", err)
	}

	return paymentdomain.ReconstituteMoney(result.FeeAmount, result.Currency), nil
}
