package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// Tests de integración de los repositorios de dispute. Requieren PostgreSQL
// con el esquema migrado; si no está disponible, se saltan. DSN por
// DATABASE_URL. Los tests con pgxmock corren siempre.

const defaultTestDSN = "postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable"

var (
	testPool *pgxpool.Pool
	testDSN  string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	testDSN = dsn
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
		t.Skip("postgres no disponible: se omiten los tests de integración de dispute")
	}
	return testPool
}

// seedDispute persiste (con el pool admin) una disputa abierta de un tenant
// nuevo y registra la limpieza de todo lo de ese tenant.
func seedDispute(t *testing.T) *domain.Dispute {
	t.Helper()
	pool := requireDB(t)
	now := time.Now().UTC()
	ref := domain.PaymentRef{ID: uuid.NewString(), TenantID: uuid.NewString(), Status: "captured", Amount: 5000, Currency: "ARS"}
	d, err := domain.NewDispute(domain.NewDisputeID(), ref, "psp", 5000, "ARS", domain.ReasonFraudulent, now.Add(24*time.Hour), now)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}
	d.PullEvents()
	if err := NewDisputeRepository(pool).Save(context.Background(), d); err != nil {
		t.Fatalf("save dispute: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM dispute_evidence WHERE dispute_id IN (SELECT id FROM disputes WHERE tenant_id=$1)`, ref.TenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM disputes WHERE tenant_id=$1`, ref.TenantID)
	})
	return d
}
