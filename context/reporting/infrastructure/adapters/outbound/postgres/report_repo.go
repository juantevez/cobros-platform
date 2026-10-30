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

// Revenue agrega las comisiones cobradas por período, por moneda.
func (r *ReportRepository) Revenue(ctx context.Context, q application.RevenueQuery) ([]domain.RevenueSummary, error) {
	args := []any{q.TenantID}
	where := "tenant_id = $1"
	where = appendTimeBounds(where, &args, q.From, q.To, "captured_at")

	sql := fmt.Sprintf(`
		SELECT currency,
		       COUNT(*)                       AS payment_count,
		       COALESCE(SUM(amount), 0)       AS gross_amount,
		       COALESCE(SUM(platform_fee), 0) AS platform_fees,
		       COALESCE(SUM(psp_fee), 0)      AS psp_fees
		FROM report_payment_fact
		WHERE %s
		GROUP BY currency
		ORDER BY currency`, where)

	var out []domain.RevenueSummary
	err := withTenant(ctx, r.pool, q.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("reporting repo: revenue: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s domain.RevenueSummary
			if err := rows.Scan(&s.Currency, &s.PaymentCount, &s.GrossAmount, &s.PlatformFees, &s.PSPFees); err != nil {
				return fmt.Errorf("reporting repo: revenue scan: %w", err)
			}
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
