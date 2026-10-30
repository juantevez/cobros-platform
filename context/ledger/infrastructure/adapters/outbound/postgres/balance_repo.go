package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
// Debe ejecutarse dentro de la misma transacción que EntryRepository.Save:
// el TxManager fija ahí el tenant que exige el RLS de account_balances.
//
// Convención de saldo:
//   - Crédito: suma al saldo (+amount)
//   - Débito:  resta del saldo (-amount)
//
// Los consumers asientan con esta convención, así que:
//   - merchant_balance positivo = la plataforma le debe ese monto al comercio.
//   - platform_fees positivo    = la plataforma ganó ese monto en comisiones.
//   - in_transit negativo       = el PSP adeuda ese monto (cuenta de activo).
func (r *pgBalanceRepository) Apply(ctx context.Context, postings []domain.Posting) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	for _, p := range lockOrder(postings) {
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

// lockOrder devuelve los postings ordenados por cuenta. Cada UPDATE bloquea la
// fila de saldo hasta el commit: si dos asientos concurrentes tocaran las
// mismas cuentas en distinto orden, cada uno esperaría al otro (deadlock).
// Con un orden global, el segundo espera al primero y sigue.
func lockOrder(postings []domain.Posting) []domain.Posting {
	sorted := slices.Clone(postings)
	slices.SortStableFunc(sorted, func(a, b domain.Posting) int {
		return strings.Compare(a.AccountID().String(), b.AccountID().String())
	})
	return sorted
}

// GetBalance retorna el saldo actual de una cuenta en centavos. Con RLS solo
// ve las cuentas del tenant del caller.
func (r *pgBalanceRepository) GetBalance(ctx context.Context, accountID domain.AccountID) (int64, error) {
	var balance int64
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn postgres.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT balance FROM account_balances WHERE account_id = $1`,
			accountID.String(),
		).Scan(&balance)
	})
	if err != nil {
		return 0, fmt.Errorf("balance repo: get balance: %w", err)
	}
	return balance, nil
}
