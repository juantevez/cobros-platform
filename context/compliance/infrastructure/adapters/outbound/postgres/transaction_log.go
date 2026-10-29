package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// TransactionLog es la proyección propia de pagos capturados
// (aml_monitored_payments), alimentada desde payment.captured.v1. Reemplaza la
// lectura cruzada de la tabla payments: Compliance solo depende del contrato
// del evento, no del esquema de Payment.
type TransactionLog struct{ pool pkgpostgres.Conn }

func NewTransactionLog(pool pkgpostgres.Conn) *TransactionLog {
	return &TransactionLog{pool: pool}
}

// Record inserta el pago; ON CONFLICT lo hace idempotente ante re-entregas.
func (l *TransactionLog) Record(ctx context.Context, p application.MonitoredPayment) error {
	conn := pkgpostgres.ConnFromContext(ctx, l.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO aml_monitored_payments (payment_id, tenant_id, amount, currency, captured_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (payment_id) DO NOTHING`,
		p.PaymentID, p.TenantID, p.Amount, p.Currency, p.CapturedAt,
	)
	if err != nil {
		return fmt.Errorf("compliance log: record payment: %w", err)
	}
	return nil
}

// CountInWindow cuenta los pagos con since < captured_at <= until (ventana
// deslizante que termina en la hora del evento evaluado).
func (l *TransactionLog) CountInWindow(ctx context.Context, tenantID string, since, until time.Time) (int, error) {
	var count int
	err := l.queryRow(ctx, tenantID, `
		SELECT COUNT(*) FROM aml_monitored_payments
		WHERE tenant_id = $1 AND captured_at > $2 AND captured_at <= $3`,
		[]any{tenantID, since, until}, &count)
	if err != nil {
		return 0, fmt.Errorf("compliance log: count in window: %w", err)
	}
	return count, nil
}

func (l *TransactionLog) SumAmountInWindow(ctx context.Context, tenantID, currency string, since, until time.Time) (int64, error) {
	var total int64
	err := l.queryRow(ctx, tenantID, `
		SELECT COALESCE(SUM(amount), 0) FROM aml_monitored_payments
		WHERE tenant_id = $1 AND currency = $2 AND captured_at > $3 AND captured_at <= $4`,
		[]any{tenantID, currency, since, until}, &total)
	if err != nil {
		return 0, fmt.Errorf("compliance log: sum in window: %w", err)
	}
	return total, nil
}

// queryRow corre una lectura escalar con app.current_tenant fijado (RLS).
func (l *TransactionLog) queryRow(ctx context.Context, tenantID, q string, args []any, dest any) error {
	ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	return pkgpostgres.RunWithAppTenant(ctx, l.pool, func(ctx context.Context) error {
		return pkgpostgres.ConnFromContext(ctx, l.pool).QueryRow(ctx, q, args...).Scan(dest)
	})
}
