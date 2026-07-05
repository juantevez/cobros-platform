package domain

import (
	"fmt"
	"time"
)

// Granularity define el bucketing temporal de las series de volumen.
type Granularity string

const (
	GranularityDay   Granularity = "day"
	GranularityWeek  Granularity = "week"
	GranularityMonth Granularity = "month"
)

// ParseGranularity valida y normaliza la granularidad pedida. Vacío = day.
func ParseGranularity(s string) (Granularity, error) {
	switch Granularity(s) {
	case "", GranularityDay:
		return GranularityDay, nil
	case GranularityWeek:
		return GranularityWeek, nil
	case GranularityMonth:
		return GranularityMonth, nil
	}
	return "", fmt.Errorf("invalid granularity: %q (use day|week|month)", s)
}

func (g Granularity) String() string { return string(g) }

// VolumePoint es un punto de la serie de volumen transaccional agregado.
type VolumePoint struct {
	Bucket       time.Time `json:"bucket"`
	Currency     string    `json:"currency"`
	PaymentCount int64     `json:"payment_count"`
	GrossAmount  int64     `json:"gross_amount"` // centavos
}

// RevenueSummary agrega las comisiones cobradas por la plataforma en un período.
type RevenueSummary struct {
	Currency     string `json:"currency"`
	PaymentCount int64  `json:"payment_count"`
	GrossAmount  int64  `json:"gross_amount"`  // centavos
	PlatformFees int64  `json:"platform_fees"` // revenue de la plataforma, centavos
	PSPFees      int64  `json:"psp_fees"`      // costo del PSP, centavos
}

// TenantBalance es el saldo neto de un tipo de cuenta del comercio, derivado
// de los movimientos del ledger. El signo depende de la convención contable
// del tipo de cuenta; aquí se expone débitos, créditos y neto por separado.
type TenantBalance struct {
	AccountType string `json:"account_type"`
	Currency    string `json:"currency"`
	Debits      int64  `json:"debits"`  // suma de débitos, centavos
	Credits     int64  `json:"credits"` // suma de créditos, centavos
	Net         int64  `json:"net"`     // debits - credits, centavos
}
