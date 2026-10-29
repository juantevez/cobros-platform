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
	// Las reglas se evalúan en la hora real de la captura: si el worker
	// procesa un backlog, la ventana no incluye pagos posteriores. Las alertas
	// de monitoreo nacen con esa misma hora.
	at := cmd.CapturedAt.UTC()
	if cmd.CapturedAt.IsZero() {
		at = uc.clock.Now()
	}

	// Proyección propia del pago (idempotente por payment_id): las reglas de
	// ventana cuentan sobre ella en lugar de leer la tabla payments.
	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.txLog.Record(ctx, MonitoredPayment{
			PaymentID: cmd.PaymentID, TenantID: cmd.TenantID,
			Amount: cmd.Amount, Currency: cmd.Currency, CapturedAt: at,
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
			at,
		)
		if err := raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert); err != nil {
			return err
		}
	}

	if uc.rules.VelocityWindow <= 0 {
		return nil // reglas de ventana desactivadas
	}
	// Ventana deslizante (since, at]. Cooldown: no se levanta otra alerta del
	// mismo tipo para el tenant si ya hubo una dentro de la ventana, así un
	// pico genera una sola alerta aunque cruce cualquier borde de reloj. El
	// subject incluye el pago que disparó la regla (re-entregas idempotentes).
	since := at.Add(-uc.rules.VelocityWindow)
	window := uc.rules.VelocityWindow.String()

	// Regla 2: velocity — demasiados pagos capturados en la ventana.
	count, err := uc.txLog.CountInWindow(ctx, cmd.TenantID, since, at)
	if err != nil {
		return err
	}
	if uc.rules.VelocityCount > 0 && count >= uc.rules.VelocityCount {
		const prefix = "velocity:"
		alert := domain.NewAlert(
			domain.NewAlertID(), tenantID,
			domain.AlertTransactionVelocity, domain.RiskHigh,
			prefix+cmd.PaymentID, 90,
			map[string]string{
				"payment_id":   cmd.PaymentID, // pago que cruzó el umbral
				"count":        strconv.Itoa(count),
				"window":       window,
				"window_start": since.Format(time.RFC3339),
				"threshold":    strconv.Itoa(uc.rules.VelocityCount),
			},
			at,
		)
		if err := raiseWindowAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert, prefix, since); err != nil {
			return err
		}
	}

	// Regla 3: volumen — monto acumulado en la ventana (por moneda).
	if uc.rules.VolumeAmount > 0 {
		total, err := uc.txLog.SumAmountInWindow(ctx, cmd.TenantID, cmd.Currency, since, at)
		if err != nil {
			return err
		}
		if total >= uc.rules.VolumeAmount {
			prefix := fmt.Sprintf("volume:%s:", cmd.Currency)
			alert := domain.NewAlert(
				domain.NewAlertID(), tenantID,
				domain.AlertTransactionVolume, domain.RiskHigh,
				prefix+cmd.PaymentID,
				riskScoreForAmount(total, uc.rules.VolumeAmount),
				map[string]string{
					"payment_id":   cmd.PaymentID, // pago que cruzó el umbral
					"total_amount": strconv.FormatInt(total, 10),
					"currency":     cmd.Currency,
					"window":       window,
					"window_start": since.Format(time.RFC3339),
					"threshold":    strconv.FormatInt(uc.rules.VolumeAmount, 10),
				},
				at,
			)
			if err := raiseWindowAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert, prefix, since); err != nil {
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
