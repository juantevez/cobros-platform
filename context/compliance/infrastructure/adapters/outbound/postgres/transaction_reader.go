package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TransactionReader cuenta pagos capturados leyendo la tabla payments
// (misma BD, patrón de lectura cruzada) para la regla de velocity.
type TransactionReader struct{ pool *pgxpool.Pool }

func NewTransactionReader(pool *pgxpool.Pool) *TransactionReader {
	return &TransactionReader{pool: pool}
}

func (r *TransactionReader) CountCapturedSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM payments
		WHERE tenant_id = $1 AND status = 'captured' AND captured_at >= $2`,
		tenantID, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("compliance reader: count captured: %w", err)
	}
	return count, nil
}
