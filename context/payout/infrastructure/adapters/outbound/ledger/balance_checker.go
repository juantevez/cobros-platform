// Package ledger provee adaptadores para consultar datos del contexto Ledger.
//
// En el monolith modular, la query va directamente a account_balances en Postgres.
// En una arquitectura de microservicios, sería una llamada HTTP o un evento.
package ledger

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// BalanceChecker implementa application.BalanceChecker consultando
// directamente la tabla account_balances del Ledger en Postgres.
type BalanceChecker struct {
	pool *pgxpool.Pool
}

func NewBalanceChecker(pool *pgxpool.Pool) *BalanceChecker {
	return &BalanceChecker{pool: pool}
}

// GetAvailableBalance retorna el saldo de la cuenta merchant_balance del tenant.
// Un saldo positivo indica que la plataforma le debe ese monto al comercio.
func (b *BalanceChecker) GetAvailableBalance(
	ctx context.Context,
	tenantID domain.TenantID,
	currency string,
) (int64, error) {
	var balance int64
	err := b.pool.QueryRow(ctx, `
		SELECT ab.balance
		FROM account_balances ab
		JOIN ledger_accounts la ON la.id = ab.account_id
		WHERE la.tenant_id = $1
		  AND la.type      = 'merchant_balance'
		  AND la.currency  = $2`,
		tenantID.String(), currency,
	).Scan(&balance)

	if err != nil {
		return 0, fmt.Errorf("balance checker: query merchant_balance: %w", err)
	}

	// balance puede ser negativo en teoría (errores contables): lo devolvemos
	// tal cual y el caso de uso decide si es suficiente para el payout.
	return balance, nil
}
