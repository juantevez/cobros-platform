package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgBalanceRepository struct {
	pool *pgxpool.Pool
}

func NewBalanceRepository(pool *pgxpool.Pool) *pgBalanceRepository {
	return &pgBalanceRepository{pool: pool}
}

// Apply actualiza los saldos de todas las cuentas afectadas por los postings.
// Debe ejecutarse dentro de la misma transacción que EntryRepository.Save.
//
// Convención de saldo:
//   - Crédito: suma al saldo (+amount)
//   - Débito:  resta del saldo (-amount)
//
// El significado del signo depende del tipo de cuenta:
//   - merchant_balance positivo = la plataforma le debe ese monto al comercio.
//   - platform_fees positivo    = la plataforma ganó ese monto en comisiones.
func (r *pgBalanceRepository) Apply(ctx context.Context, postings []domain.Posting) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	for _, p := range postings {
		delta := p.Money().Amount()
		if p.IsDebit() {
			delta = -delta
		}

		tag, err := conn.Exec(ctx, `
			UPDATE account_balances
			SET balance    = balance + $2,
			    updated_at = $3
			WHERE account_id = $1`,
			p.AccountID().String(),
			delta,
			time.Now().UTC(),
		)
		if err != nil {
			return fmt.Errorf("balance repo: apply posting %s: %w", p.ID(), err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("balance repo: account %s not found in account_balances", p.AccountID())
		}
	}
	return nil
}

// GetBalance retorna el saldo actual de una cuenta en centavos.
func (r *pgBalanceRepository) GetBalance(ctx context.Context, accountID domain.AccountID) (int64, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	var balance int64
	err := conn.QueryRow(ctx, `
		SELECT balance FROM account_balances WHERE account_id = $1`,
		accountID.String(),
	).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("balance repo: get balance: %w", err)
	}
	return balance, nil
}
