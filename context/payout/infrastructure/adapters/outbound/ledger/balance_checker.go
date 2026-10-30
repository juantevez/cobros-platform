// Package ledger provee adaptadores para consultar datos del contexto Ledger.
//
// En el monolith modular, la query va directamente a account_balances en Postgres.
// En una arquitectura de microservicios, sería una llamada HTTP o un evento.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// BalanceChecker implementa application.BalanceChecker consultando
// directamente la tabla account_balances del Ledger en Postgres.
type BalanceChecker struct {
	pool pkgpostgres.Conn
}

func NewBalanceChecker(pool pkgpostgres.Conn) *BalanceChecker {
	return &BalanceChecker{pool: pool}
}

// GetAvailableBalance retorna el saldo de la cuenta merchant_balance del tenant
// menos los payouts que todavía no se descontaron de ella.
// Un saldo positivo indica que la plataforma le debe ese monto al comercio.
//
// El Ledger debita merchant_balance de forma asíncrona (outbox → consumer de
// payout.initiated.v1): hasta que el asiento existe, el saldo todavía incluye
// el payout. Sin descontarlo, dos payouts seguidos leían el mismo saldo y
// ambos pasaban aunque el lock los serializara. Se descuentan los payouts sin
// su asiento de initiated (ledger_entry_key), salvo los fallidos: su reversa
// tampoco llegó, así que el efecto neto pendiente es cero.
func (b *BalanceChecker) GetAvailableBalance(
	ctx context.Context,
	tenantID domain.TenantID,
	currency string,
) (int64, error) {
	var balance int64
	err := b.pool.QueryRow(ctx, `
		SELECT ab.balance - COALESCE((
			SELECT SUM(p.amount)
			FROM payouts p
			WHERE p.tenant_id = la.tenant_id
			  AND p.currency  = la.currency
			  AND p.status   <> 'failed'
			  AND NOT EXISTS (
			      SELECT 1 FROM journal_entries je
			      WHERE je.tenant_id       = p.tenant_id
			        AND je.idempotency_key = p.ledger_entry_key
			  )
		), 0)
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

// LockBalance implementa application.BalanceLocker: toma un
// pg_advisory_xact_lock keyed por (tenant, currency) sobre la transacción
// activa del contexto. El lock se libera automáticamente con commit/rollback.
//
// Dos payouts concurrentes del mismo tenant/moneda serializan aquí la lectura
// de saldo + INSERT del payout, cerrando la ventana TOCTOU que permitía
// sobregirar merchant_balance. Si no hay tx activa (uso fuera de RunInTx),
// devuelve error: un lock de sesión sería incorrecto y enmascararía el bug.
func (b *BalanceChecker) LockBalance(
	ctx context.Context,
	tenantID domain.TenantID,
	currency string,
) error {
	tx, ok := pkgpostgres.TxFromContext(ctx)
	if !ok {
		return errors.New("balance locker: LockBalance requires an active transaction")
	}
	class, object := advisoryLockKeys(tenantID.String(), currency)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, class, object); err != nil {
		return fmt.Errorf("balance locker: advisory lock: %w", err)
	}
	return nil
}

// advisoryLockKeys deriva dos int64 estables de (tenant, currency) para usar
// como (classid, objid) de pg_advisory_xact_lock. FNV-1a es determinista entre
// procesos; la colisión es posible pero sólo causa serialización espuria
// inofensiva — nunca permite dos payouts simultáneos del mismo par real.
func advisoryLockKeys(tenantID, currency string) (class, object int64) {
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte("payout_balance|" + tenantID))
	h2 := fnv.New64a()
	_, _ = h2.Write([]byte("payout_balance|" + strings.ToUpper(strings.TrimSpace(currency))))
	return int64(h1.Sum64()), int64(h2.Sum64()) //nolint:gosec // hash → int64, overflow definido
}
