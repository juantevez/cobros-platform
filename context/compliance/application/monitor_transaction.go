package application

import (
	"context"
	"strconv"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// MonitoringRules configura los umbrales de las reglas de monitoreo transaccional.
type MonitoringRules struct {
	ThresholdAmount int64         // monto (centavos) a partir del cual se alerta
	VelocityCount   int           // Nº de pagos capturados en la ventana que dispara alerta
	VelocityWindow  time.Duration // ventana temporal de la regla de velocity
}

// DefaultMonitoringRules retorna umbrales razonables para desarrollo.
func DefaultMonitoringRules() MonitoringRules {
	return MonitoringRules{
		ThresholdAmount: 1_000_000,       // $10.000,00
		VelocityCount:   10,              // 10 pagos
		VelocityWindow:  10 * time.Minute, // en 10 minutos
	}
}

// MonitorTransactionUseCase evalúa un pago capturado contra las reglas de AML.
// Cada regla disparada genera una alerta independiente (idempotente por subject).
type MonitorTransactionUseCase struct {
	repo      AlertRepository
	txReader  TransactionReader
	txManager TxManager
	publisher EventPublisher
	clock     Clock
	rules     MonitoringRules
}

func NewMonitorTransactionUseCase(
	repo AlertRepository,
	txReader TransactionReader,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
	rules MonitoringRules,
) *MonitorTransactionUseCase {
	return &MonitorTransactionUseCase{
		repo: repo, txReader: txReader, txManager: txManager,
		publisher: publisher, clock: clock, rules: rules,
	}
}

func (uc *MonitorTransactionUseCase) Execute(ctx context.Context, cmd MonitorTransactionCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	now := uc.clock.Now()

	// Regla 1: monto por encima del umbral.
	if cmd.Amount >= uc.rules.ThresholdAmount {
		alert := domain.NewAlert(
			domain.NewAlertID(), tenantID,
			domain.AlertTransactionThreshold, domain.RiskMedium,
			cmd.PaymentID, riskScoreForAmount(cmd.Amount, uc.rules.ThresholdAmount),
			map[string]string{
				"payment_id":     cmd.PaymentID,
				"amount":         strconv.FormatInt(cmd.Amount, 10),
				"currency":       cmd.Currency,
				"threshold":      strconv.FormatInt(uc.rules.ThresholdAmount, 10),
				"payment_method": cmd.PaymentMethod,
			},
			now,
		)
		if err := raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert); err != nil {
			return err
		}
	}

	// Regla 2: velocity — demasiados pagos capturados en la ventana.
	since := now.Add(-uc.rules.VelocityWindow)
	count, err := uc.txReader.CountCapturedSince(ctx, cmd.TenantID, since)
	if err != nil {
		return err
	}
	if count >= uc.rules.VelocityCount {
		alert := domain.NewAlert(
			domain.NewAlertID(), tenantID,
			domain.AlertTransactionVelocity, domain.RiskHigh,
			cmd.PaymentID, 90,
			map[string]string{
				"payment_id": cmd.PaymentID,
				"count":      strconv.Itoa(count),
				"window":     uc.rules.VelocityWindow.String(),
				"threshold":  strconv.Itoa(uc.rules.VelocityCount),
			},
			now,
		)
		if err := raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert); err != nil {
			return err
		}
	}

	return nil
}

// riskScoreForAmount escala el puntaje según cuánto supera el umbral (60..100).
func riskScoreForAmount(amount, threshold int64) int {
	if threshold <= 0 {
		return 80
	}
	ratio := amount / threshold
	score := 60 + int(ratio)*10
	if score > 100 {
		score = 100
	}
	return score
}
