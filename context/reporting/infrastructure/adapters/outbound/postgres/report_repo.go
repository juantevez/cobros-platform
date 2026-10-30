package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// ReportRepository ejecuta las consultas agregadas del dashboard.
//
// Cada consulta filtra por tenant_id y corre en una sesión con
// app.current_tenant fijado: el WHERE explícito y la RLS aíslan por comercio.
type ReportRepository struct{ pool pkgpostgres.Conn }

func NewReportRepository(pool pkgpostgres.Conn) *ReportRepository {
	return &ReportRepository{pool: pool}
}

// Volume agrega el volumen transaccional por bucket temporal y moneda.
func (r *ReportRepository) Volume(ctx context.Context, q application.VolumeQuery) ([]domain.VolumePoint, error) {
	args := []any{q.TenantID, q.Granularity.String()}
	where := "tenant_id = $1"
	where = appendTimeBounds(where, &args, q.From, q.To, "captured_at")

	sql := fmt.Sprintf(`
		SELECT date_trunc($2, captured_at) AS bucket, currency,
		       COUNT(*)          AS payment_count,
		       COALESCE(SUM(amount), 0) AS gross_amount
		FROM report_payment_fact
		WHERE %s
		GROUP BY bucket, currency
		ORDER BY bucket, currency`, where)

	var out []domain.VolumePoint
	err := withTenant(ctx, r.pool, q.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("reporting repo: volume: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var p domain.VolumePoint
			if err := rows.Scan(&p.Bucket, &p.Currency, &p.PaymentCount, &p.GrossAmount); err != nil {
				return fmt.Errorf("reporting repo: volume scan: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Revenue agrega por moneda lo capturado en el período (bruto y comisiones)
// y los reembolsos y contracargos que ocurrieron en el período. Una moneda
// que solo tiene ajustes aparece igual, con bruto cero.
func (r *ReportRepository) Revenue(ctx context.Context, q application.RevenueQuery) ([]domain.RevenueSummary, error) {
	args := []any{q.TenantID}
	paymentsWhere := appendTimeBounds("tenant_id = $1", &args, q.From, q.To, "captured_at")
	adjustmentsWhere := appendTimeBounds("tenant_id = $1", &args, q.From, q.To, "occurred_at")

	sql := fmt.Sprintf(`
		WITH p AS (
			SELECT currency,
			       COUNT(*)          AS payment_count,
			       SUM(amount)       AS gross_amount,
			       SUM(platform_fee) AS platform_fees,
			       SUM(psp_fee)      AS psp_fees
			FROM report_payment_fact
			WHERE %s
			GROUP BY currency
		), a AS (
			SELECT currency,
			       COUNT(*) FILTER (WHERE kind = 'refund')                  AS refund_count,
			       COALESCE(SUM(amount) FILTER (WHERE kind = 'refund'), 0)     AS refunded_amount,
			       COUNT(*) FILTER (WHERE kind = 'chargeback')              AS chargeback_count,
			       COALESCE(SUM(amount) FILTER (WHERE kind = 'chargeback'), 0) AS chargeback_amount
			FROM report_payment_adjustment
			WHERE %s
			GROUP BY currency
		)
		SELECT currency,
		       COALESCE(p.payment_count, 0), COALESCE(p.gross_amount, 0),
		       COALESCE(p.platform_fees, 0), COALESCE(p.psp_fees, 0),
		       COALESCE(a.refund_count, 0), COALESCE(a.refunded_amount, 0),
		       COALESCE(a.chargeback_count, 0), COALESCE(a.chargeback_amount, 0)
		FROM p FULL OUTER JOIN a USING (currency)
		ORDER BY currency`, paymentsWhere, adjustmentsWhere)

	var out []domain.RevenueSummary
	err := withTenant(ctx, r.pool, q.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("reporting repo: revenue: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s domain.RevenueSummary
			if err := rows.Scan(
				&s.Currency, &s.PaymentCount, &s.GrossAmount, &s.PlatformFees, &s.PSPFees,
				&s.RefundCount, &s.RefundedAmount, &s.ChargebackCount, &s.ChargebackAmount,
			); err != nil {
				return fmt.Errorf("reporting repo: revenue scan: %w", err)
			}
			s.NetAmount = s.GrossAmount - s.RefundedAmount - s.ChargebackAmount
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Balances calcula el saldo neto por tipo de cuenta y moneda del comercio.
func (r *ReportRepository) Balances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error) {
	var out []domain.TenantBalance
	err := withTenant(ctx, r.pool, tenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, `
			SELECT account_type, currency,
			       COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0) AS debits,
			       COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0) AS credits
			FROM report_ledger_movement
			WHERE tenant_id = $1
			GROUP BY account_type, currency
			ORDER BY account_type, currency`,
			tenantID,
		)
		if err != nil {
			return fmt.Errorf("reporting repo: balances: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var b domain.TenantBalance
			if err := rows.Scan(&b.AccountType, &b.Currency, &b.Debits, &b.Credits); err != nil {
				return fmt.Errorf("reporting repo: balances scan: %w", err)
			}
			b.Net = b.Debits - b.Credits
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// appendTimeBounds agrega condiciones de rango temporal a la cláusula WHERE
// solo para los límites no-cero, ampliando args con los parámetros posicionales.
// El rango es semiabierto [from, to): períodos contiguos (ej. 09-01..10-01 y
// 10-01..11-01) no cuentan dos veces un hecho ocurrido justo en el límite.
func appendTimeBounds(where string, args *[]any, from, to time.Time, col string) string {
	if !from.IsZero() {
		*args = append(*args, from)
		where += fmt.Sprintf(" AND %s >= $%d", col, len(*args))
	}
	if !to.IsZero() {
		*args = append(*args, to)
		where += fmt.Sprintf(" AND %s < $%d", col, len(*args))
	}
	return where
}
