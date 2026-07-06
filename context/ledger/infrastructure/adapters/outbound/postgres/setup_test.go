package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// setup_test.go: infraestructura de los tests de integración de los repos del
// Ledger. Requieren PostgreSQL con el esquema migrado; si no está disponible,
// los tests se saltan. DSN configurable por DATABASE_URL.

const defaultTestDSN = "postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable"

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	if pool, err := pkgpostgres.New(ctx, pkgpostgres.DefaultConfig(dsn)); err == nil {
		if pool.Ping(ctx) == nil {
			testPool = pool
		}
	}
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testPool == nil {
		t.Skip("postgres no disponible: se omiten los tests de integración del ledger")
	}
	return testPool
}

func testTenantID(t *testing.T) domain.TenantID {
	t.Helper()
	id, err := domain.ParseTenantID(uuid.NewString())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return id
}

// seedAccount crea una cuenta contable (con su balance en 0) y programa limpieza.
func seedAccount(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID, at domain.AccountType, currency string) *domain.Account {
	t.Helper()
	acc, err := domain.NewAccount(domain.NewAccountID(), tenantID, at, currency, "cuenta de test")
	if err != nil {
		t.Fatalf("build account: %v", err)
	}
	acc.PullEvents()
	if err := NewAccountRepository(pool).Save(context.Background(), acc); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	cleanupTenant(t, pool, tenantID)
	return acc
}

// saveEntryTx guarda un asiento dentro de una transacción real (el trigger de
// doble partida es DEFERRABLE: valida al commit).
func saveEntryTx(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID, e *domain.JournalEntry) error {
	t.Helper()
	tm := pkgpostgres.NewTxManager(pool)
	ctx := pkgpostgres.WithTenantID(context.Background(), tenantID.String())
	repo := NewEntryRepository(pool)
	return tm.RunInTx(ctx, func(ctx context.Context) error {
		return repo.Save(ctx, e)
	})
}

// buildBalancedEntry construye un asiento balanceado (debit acc1 / credit acc2).
func buildBalancedEntry(t *testing.T, tenantID domain.TenantID, key string, accDebit, accCredit domain.AccountID) *domain.JournalEntry {
	t.Helper()
	e, err := domain.NewJournalEntry(
		domain.NewEntryID(), tenantID, key, "asiento de test",
		map[string]string{"source": "integration-test"},
		time.Now().UTC(),
		[]domain.PostingInput{
			{AccountID: accDebit, Direction: domain.DirectionDebit, Amount: 100, Currency: "ARS"},
			{AccountID: accCredit, Direction: domain.DirectionCredit, Amount: 100, Currency: "ARS"},
		},
	)
	if err != nil {
		t.Fatalf("build entry: %v", err)
	}
	e.PullEvents()
	return e
}

// cleanupTenant borra todo el subárbol contable del tenant tras el test.
func cleanupTenant(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		id := tenantID.String()
		_, _ = pool.Exec(ctx, `DELETE FROM postings WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM journal_entries WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM account_balances WHERE account_id IN (SELECT id FROM ledger_accounts WHERE tenant_id=$1)`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_accounts WHERE tenant_id=$1`, id)
	})
}

func timesClose(t *testing.T, got, want time.Time) {
	t.Helper()
	if d := got.Sub(want); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("timestamps differ: got %v want %v", got, want)
	}
}
