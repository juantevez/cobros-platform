package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/billing/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// setup_test.go: infraestructura de los tests de integración de los repos de
// Billing. Requieren PostgreSQL con el esquema migrado; si no está disponible,
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
		t.Skip("postgres no disponible: se omiten los tests de integración de billing")
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

// seedPlan crea un PricingPlan en la base y programa su limpieza.
func seedPlan(t *testing.T, pool *pgxpool.Pool, name string, baseRateBps, baseFixed int64) *domain.PricingPlan {
	t.Helper()
	p, err := domain.NewPricingPlan(domain.NewPlanID(), name, "desc", baseRateBps, baseFixed, 0, "ARS")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	p.PullEvents()
	if err := NewPlanRepository(pool).Save(context.Background(), p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	cleanupPlan(t, pool, p.ID())
	return p
}

func cleanupPlan(t *testing.T, pool *pgxpool.Pool, id domain.PlanID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM billing_plan_method_rates WHERE plan_id=$1`, id.String())
		_, _ = pool.Exec(ctx, `DELETE FROM billing_tenant_plans WHERE plan_id=$1`, id.String())
		_, _ = pool.Exec(ctx, `DELETE FROM billing_plans WHERE id=$1`, id.String())
	})
}

func cleanupTenant(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM billing_tenant_plans WHERE tenant_id=$1`, tenantID.String())
	})
}
