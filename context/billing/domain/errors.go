package domain

import "errors"

var (
	ErrPlanNotFound          = errors.New("pricing plan not found")
	ErrPlanInactive          = errors.New("pricing plan is inactive")
	ErrPlanNameEmpty         = errors.New("plan name cannot be empty")
	ErrInvalidRateBps        = errors.New("rate must be between 0 and 10000 basis points (0% - 100%)")
	ErrInvalidFixedAmount    = errors.New("fixed fee amount cannot be negative")
	ErrInvalidMonthlyFee     = errors.New("monthly fee cannot be negative")
	ErrInvalidCurrency       = errors.New("invalid currency code")
	ErrTenantPlanNotFound    = errors.New("tenant has no active pricing plan assigned")
	ErrTenantPlanAlreadySet  = errors.New("tenant already has an active plan; deactivate it first")
	ErrInvalidPaymentMethod  = errors.New("invalid payment method for rate override")
	ErrFeeCalculationInvalid = errors.New("fee calculation resulted in invalid amount")
)
