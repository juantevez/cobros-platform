package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

// ReportRepository ejecuta las consultas agregadas del dashboard.
//
// El aislamiento por comercio se garantiza con el filtro explícito
// WHERE tenant_id=$1 (el rol de la app es owner y no está sujeto a RLS;
// RLS actúa como defensa en profundidad para las escrituras transaccionales).
type ReportRepository struct{ pool *pgxpool.Pool }

func NewReportRepository(pool *pgxpool.Pool) *ReportRepository {
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

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("reporting repo: volume: %w", err)
	}
	defer rows.Close()

	var out []domain.VolumePoint
	for rows.Next() {
		var p domain.VolumePoint
		if err := rows.Scan(&p.Bucket, &p.Currency, &p.PaymentCount, &p.GrossAmount); err != nil {
			return nil, fmt.Errorf("reporting repo: volume scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
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

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("reporting repo: revenue: %w", err)
	}
	defer rows.Close()

	var out []domain.RevenueSummary
	for rows.Next() {
		var s domain.RevenueSummary
		if err := rows.Scan(&s.Currency, &s.PaymentCount, &s.GrossAmount, &s.PlatformFees, &s.PSPFees); err != nil {
			return nil, fmt.Errorf("reporting repo: revenue scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Balances calcula el saldo neto por tipo de cuenta y moneda del comercio.
func (r *ReportRepository) Balances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error) {
	rows, err := r.pool.Query(ctx, `
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
		return nil, fmt.Errorf("reporting repo: balances: %w", err)
	}
	defer rows.Close()

	var out []domain.TenantBalance
	for rows.Next() {
		var b domain.TenantBalance
		if err := rows.Scan(&b.AccountType, &b.Currency, &b.Debits, &b.Credits); err != nil {
			return nil, fmt.Errorf("reporting repo: balances scan: %w", err)
		}
		b.Net = b.Debits - b.Credits
		out = append(out, b)
	}
	return out, rows.Err()
}

// appendTimeBounds agrega condiciones de rango temporal a la cláusula WHERE
// solo para los límites no-cero, ampliando args con los parámetros posicionales.
func appendTimeBounds(where string, args *[]any, from, to time.Time, col string) string {
	if !from.IsZero() {
		*args = append(*args, from)
		where += fmt.Sprintf(" AND %s >= $%d", col, len(*args))
	}
	if !to.IsZero() {
		*args = append(*args, to)
		where += fmt.Sprintf(" AND %s <= $%d", col, len(*args))
	}
	return where
}
