package readers

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// PaymentReader implementa application.PaymentReader consultando
// directamente la tabla payments (mismo esquema PostgreSQL).
type PaymentReader struct {
	pool pkgpostgres.Conn
}

func NewPaymentReader(pool pkgpostgres.Conn) *PaymentReader {
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

	var payments []application.SystemPayment
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("payment reader: query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var p application.SystemPayment
			if err := rows.Scan(
				&p.PaymentID, &p.PSPReference,
				&p.Amount, &p.Currency,
				&p.Status, &p.CapturedAt,
			); err != nil {
				return fmt.Errorf("payment reader: scan: %w", err)
			}
			payments = append(payments, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return payments, nil
}

// LedgerChecker implementa application.LedgerChecker consultando
// directamente journal_entries, postings y payments (mismo esquema).
type LedgerChecker struct {
	pool pkgpostgres.Conn
}

func NewLedgerChecker(pool pkgpostgres.Conn) *LedgerChecker {
	return &LedgerChecker{pool: pool}
}

// Check revisa los asientos creados en el período (balance por moneda) y los
// pagos capturados en el período (asiento payment_captured_<id>, la clave con
// la que los asienta el consumer de Ledger).
func (c *LedgerChecker) Check(
	ctx context.Context,
	tenantID domain.TenantID,
	from, to time.Time,
) (application.LedgerCheck, error) {
	var check application.LedgerCheck
	tid := tenantID.String()

	err := withTenant(ctx, c.pool, tid, func(ctx context.Context, conn pkgpostgres.Conn) error {
		entriesFilter, args := periodFilter("je.created_at", "je.tenant_id", tid, from, to)
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM journal_entries je WHERE `+entriesFilter, args...,
		).Scan(&check.EntriesChecked); err != nil {
			return fmt.Errorf("ledger checker: count entries: %w", err)
		}

		rows, err := conn.Query(ctx, `
			SELECT je.id, COALESCE(p.currency, ''),
			       COALESCE(SUM(p.amount) FILTER (WHERE p.direction = 'credit'), 0) -
			       COALESCE(SUM(p.amount) FILTER (WHERE p.direction = 'debit'), 0)
			FROM journal_entries je
			LEFT JOIN postings p ON p.entry_id = je.id
			WHERE `+entriesFilter+`
			GROUP BY je.id, p.currency
			HAVING count(p.id) = 0
			    OR COALESCE(SUM(p.amount) FILTER (WHERE p.direction = 'credit'), 0) <>
			       COALESCE(SUM(p.amount) FILTER (WHERE p.direction = 'debit'), 0)
			ORDER BY je.id, 2`, args...)
		if err != nil {
			return fmt.Errorf("ledger checker: unbalanced entries: %w", err)
		}
		for rows.Next() {
			var u application.LedgerImbalance
			if err := rows.Scan(&u.EntryID, &u.Currency, &u.Imbalance); err != nil {
				rows.Close()
				return fmt.Errorf("ledger checker: scan entry: %w", err)
			}
			check.Unbalanced = append(check.Unbalanced, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("ledger checker: unbalanced entries: %w", err)
		}

		capturesFilter, args := periodFilter("p.captured_at", "p.tenant_id", tid, from, to)
		capturesFilter += ` AND p.status IN ('captured', 'refunded')`
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM payments p WHERE `+capturesFilter, args...,
		).Scan(&check.CapturesChecked); err != nil {
			return fmt.Errorf("ledger checker: count captures: %w", err)
		}

		rows, err = conn.Query(ctx, `
			SELECT p.id, COALESCE(p.psp_reference, ''), p.amount, p.currency, p.status, p.captured_at
			FROM payments p
			WHERE `+capturesFilter+`
			  AND NOT EXISTS (
			      SELECT 1 FROM journal_entries je
			      WHERE je.tenant_id = p.tenant_id
			        AND je.idempotency_key = 'payment_captured_' || p.id::text)
			ORDER BY p.captured_at, p.id`, args...)
		if err != nil {
			return fmt.Errorf("ledger checker: captures without entry: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p application.SystemPayment
			if err := rows.Scan(&p.PaymentID, &p.PSPReference, &p.Amount, &p.Currency, &p.Status, &p.CapturedAt); err != nil {
				return fmt.Errorf("ledger checker: scan capture: %w", err)
			}
			check.MissingEntries = append(check.MissingEntries, p)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("ledger checker: captures without entry: %w", err)
		}
		return nil
	})
	if err != nil {
		return application.LedgerCheck{}, err
	}
	return check, nil
}

// periodFilter arma "<ts> >= $1 AND <ts> < $2" y, si hay tenant, el filtro
// por tenant como $3.
func periodFilter(tsCol, tenantCol, tenantID string, from, to time.Time) (string, []any) {
	filter := tsCol + " >= $1 AND " + tsCol + " < $2"
	args := []any{from, to}
	if tenantID != "" {
		filter += " AND " + tenantCol + " = $3"
		args = append(args, tenantID)
	}
	return filter, args
}
