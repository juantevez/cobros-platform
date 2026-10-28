package application

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// MonitoringRules configura los umbrales de las reglas de monitoreo transaccional.
type MonitoringRules struct {
	ThresholdAmount int64         // monto (centavos) a partir del cual se alerta
	VelocityCount   int           // Nº de pagos capturados en la ventana que dispara alerta
	VelocityWindow  time.Duration // ventana temporal de las reglas de velocity y volumen
	VolumeAmount    int64         // monto acumulado (centavos, por moneda) en la ventana; 0 = regla desactivada
}

// DefaultMonitoringRules retorna umbrales razonables para desarrollo.
func DefaultMonitoringRules() MonitoringRules {
	return MonitoringRules{
		ThresholdAmount: 1_000_000,        // $10.000,00
		VelocityCount:   10,               // 10 pagos
		VelocityWindow:  10 * time.Minute, // en 10 minutos
		VolumeAmount:    5_000_000,        // $50.000,00 acumulados en la ventana
	}
}

// MonitorTransactionUseCase evalúa un pago capturado contra las reglas de AML.
// Cada regla disparada genera una alerta independiente (idempotente por subject).
type MonitorTransactionUseCase struct {
	repo      AlertRepository
	txLog     TransactionLog
	txManager TxManager
	publisher EventPublisher
	clock     Clock
	rules     MonitoringRules
}

func NewMonitorTransactionUseCase(
	repo AlertRepository,
	txLog TransactionLog,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
	rules MonitoringRules,
) *MonitorTransactionUseCase {
	return &MonitorTransactionUseCase{
		repo: repo, txLog: txLog, txManager: txManager,
		publisher: publisher, clock: clock, rules: rules,
	}
}

func (uc *MonitorTransactionUseCase) Execute(ctx context.Context, cmd MonitorTransactionCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	now := uc.clock.Now()

	// Proyección propia del pago (idempotente por payment_id): las reglas de
	// ventana cuentan sobre ella en lugar de leer la tabla payments.
	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.txLog.Record(ctx, MonitoredPayment{
			PaymentID: cmd.PaymentID, TenantID: cmd.TenantID,
			Amount: cmd.Amount, Currency: cmd.Currency, CapturedAt: now,
		})
	}); err != nil {
		return err
	}

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

	if uc.rules.VelocityWindow <= 0 {
		return nil // reglas de ventana desactivadas
	}
	since := now.Add(-uc.rules.VelocityWindow)
	// Las alertas de ventana usan como subject el bucket fijo de la ventana:
	// la unicidad (tenant, tipo, subject) deja una sola alerta por tenant y
	// ventana en lugar de una por cada pago posterior al umbral.
	bucket := now.Truncate(uc.rules.VelocityWindow).UTC().Format(time.RFC3339)

	// Regla 2: velocity — demasiados pagos capturados en la ventana.
	count, err := uc.txLog.CountSince(ctx, cmd.TenantID, since)
	if err != nil {
		return err
	}
	if uc.rules.VelocityCount > 0 && count >= uc.rules.VelocityCount {
		alert := domain.NewAlert(
			domain.NewAlertID(), tenantID,
			domain.AlertTransactionVelocity, domain.RiskHigh,
			"velocity:"+bucket, 90,
			map[string]string{
				"payment_id":   cmd.PaymentID, // pago que cruzó el umbral
				"count":        strconv.Itoa(count),
				"window":       uc.rules.VelocityWindow.String(),
				"window_start": bucket,
				"threshold":    strconv.Itoa(uc.rules.VelocityCount),
			},
			now,
		)
		if err := raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert); err != nil {
			return err
		}
	}

	// Regla 3: volumen — monto acumulado en la ventana (por moneda).
	if uc.rules.VolumeAmount > 0 {
		total, err := uc.txLog.SumAmountSince(ctx, cmd.TenantID, cmd.Currency, since)
		if err != nil {
			return err
		}
		if total >= uc.rules.VolumeAmount {
			alert := domain.NewAlert(
				domain.NewAlertID(), tenantID,
				domain.AlertTransactionVolume, domain.RiskHigh,
				fmt.Sprintf("volume:%s:%s", cmd.Currency, bucket),
				riskScoreForAmount(total, uc.rules.VolumeAmount),
				map[string]string{
					"payment_id":   cmd.PaymentID, // pago que cruzó el umbral
					"total_amount": strconv.FormatInt(total, 10),
					"currency":     cmd.Currency,
					"window":       uc.rules.VelocityWindow.String(),
					"window_start": bucket,
					"threshold":    strconv.FormatInt(uc.rules.VolumeAmount, 10),
				},
				now,
			)
			if err := raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert); err != nil {
				return err
			}
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
