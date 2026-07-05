package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AccountReader resuelve el tipo de una cuenta contable consultando
// directamente la tabla ledger_accounts (misma BD, patrón de lectura cruzada).
type AccountReader struct{ pool *pgxpool.Pool }

func NewAccountReader(pool *pgxpool.Pool) *AccountReader {
	return &AccountReader{pool: pool}
}

// AccountType retorna el tipo de la cuenta. Si la cuenta no existe, retorna
// tipo vacío sin error: el movimiento se proyecta igual (no se pierde el hecho).
func (r *AccountReader) AccountType(ctx context.Context, accountID string) (string, error) {
	var accountType string
	err := r.pool.QueryRow(ctx,
		`SELECT type FROM ledger_accounts WHERE id = $1`, accountID,
	).Scan(&accountType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reporting: account type lookup: %w", err)
	}
	return accountType, nil
}
