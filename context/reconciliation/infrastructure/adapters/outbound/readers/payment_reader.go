package readers

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// PaymentReader implementa application.PaymentReader consultando
// directamente la tabla payments (mismo esquema PostgreSQL).
type PaymentReader struct {
	pool *pgxpool.Pool
}

func NewPaymentReader(pool *pgxpool.Pool) *PaymentReader {
	return &PaymentReader{pool: pool}
}

// ReadByPeriod retorna los pagos capturados del tenant en el período dado.
// Solo incluye pagos con psp_reference (los que llegaron al PSP).
func (r *PaymentReader) ReadByPeriod(
	ctx context.Context,
	tenantID domain.TenantID,
	from, to time.Time,
) ([]application.SystemPayment, error) {
	query := `
		SELECT id, COALESCE(psp_reference,''), amount, currency,
		       status, captured_at
		FROM payments
		WHERE created_at >= $1 AND created_at < $2`

	var args []any
	args = append(args, from, to)

	if tid := tenantID.String(); tid != "" {
		query += fmt.Sprintf(" AND tenant_id=$%d", len(args)+1)
		args = append(args, tid)
	}

	query += " ORDER BY created_at"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("payment reader: query: %w", err)
	}
	defer rows.Close()

	var payments []application.SystemPayment
	for rows.Next() {
		var p application.SystemPayment
		if err := rows.Scan(
			&p.PaymentID, &p.PSPReference,
			&p.Amount, &p.Currency,
			&p.Status, &p.CapturedAt,
		); err != nil {
			return nil, fmt.Errorf("payment reader: scan: %w", err)
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

// LedgerChecker implementa application.LedgerChecker consultando
// directamente la tabla postings del Ledger.
type LedgerChecker struct {
	pool *pgxpool.Pool
}

func NewLedgerChecker(pool *pgxpool.Pool) *LedgerChecker {
	return &LedgerChecker{pool: pool}
}

// CheckBalance calcula el imbalance del Ledger para el período.
//
// Un Ledger perfectamente balanceado tiene:
//   sum(amount WHERE direction='credit') == sum(amount WHERE direction='debit')
//
// Retorna la diferencia (créditos - débitos). 0 = perfectamente balanceado.
func (c *LedgerChecker) CheckBalance(
	ctx context.Context,
	tenantID domain.TenantID,
	from, to time.Time,
) (int64, error) {
	query := `
		SELECT
			COALESCE(SUM(p.amount) FILTER (WHERE p.direction='credit'), 0) -
			COALESCE(SUM(p.amount) FILTER (WHERE p.direction='debit'),  0) AS imbalance
		FROM postings p
		JOIN journal_entries je ON je.id = p.entry_id
		WHERE je.created_at >= $1 AND je.created_at < $2`

	args := []any{from, to}
	if tid := tenantID.String(); tid != "" {
		query += fmt.Sprintf(" AND je.tenant_id=$%d", len(args)+1)
		args = append(args, tid)
	}

	var imbalance int64
	if err := c.pool.QueryRow(ctx, query, args...).Scan(&imbalance); err != nil {
		return 0, fmt.Errorf("ledger checker: %w", err)
	}
	return imbalance, nil
}
