package ledger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// Integración contra PostgreSQL con el esquema migrado (DATABASE_URL); se salta
// si no hay base. Verifica el SQL del saldo disponible, que pgxmock no ejecuta.

const defaultTestDSN = "postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable"

func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx := context.Background()
	pool, err := pkgpostgres.New(ctx, pkgpostgres.DefaultConfig(dsn))
	if err != nil || pool.Ping(ctx) != nil {
		t.Skip("postgres no disponible: se omite la integración del balance checker")
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func TestBalanceChecker_SubtractsPayoutsNotYetInLedger(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	account := uuid.NewString()
	now := time.Now().UTC()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payouts WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM journal_entries WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM account_balances WHERE account_id=$1`, account)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_accounts WHERE tenant_id=$1`, tenant)
	})

	mustExec(t, pool, `INSERT INTO ledger_accounts (id, tenant_id, type, currency, created_at)
		VALUES ($1, $2, 'merchant_balance', 'ARS', $3)`, account, tenant, now)
	mustExec(t, pool, `INSERT INTO account_balances (account_id, tenant_id, balance, updated_at) VALUES ($1, $2, 1000, $3)`, account, tenant, now)

	payout := func(amount int64, status, currency string) string {
		id := uuid.NewString()
		mustExec(t, pool, `INSERT INTO payouts (id, tenant_id, amount, currency, bank_acct_type, bank_acct_num,
				holder_name, status, ledger_entry_key, idempotency_key, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'cbu', '123', 'Comercio', $5, $6, $8, $7, $7)`,
			id, tenant, amount, currency, status, "payout_initiated_"+id, now, "key_"+id)
		return id
	}
	posted := func(payoutID string) {
		mustExec(t, pool, `INSERT INTO journal_entries (id, tenant_id, idempotency_key, occurred_at, created_at)
			VALUES ($1, $2, $3, $4, $4)`, uuid.NewString(), tenant, "payout_initiated_"+payoutID, now)
	}

	payout(300, "initiated", "ARS")         // sin asiento: se descuenta
	posted(payout(200, "confirmed", "ARS")) // ya en merchant_balance: no
	payout(100, "failed", "ARS")            // reversa pendiente también: no
	payout(50, "unknown", "ARS")            // sin asiento: se descuenta
	payout(70, "initiated", "USD")          // otra moneda: no

	tid, err := domain.ParseTenantID(tenant)
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	got, err := NewBalanceChecker(pool).GetAvailableBalance(ctx, tid, "ARS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 650 {
		t.Errorf("available = %d, want 650 (1000 - 300 - 50)", got)
	}
}
