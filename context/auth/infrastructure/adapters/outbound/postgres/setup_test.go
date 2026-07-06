package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// setup_test.go provee la infraestructura de los tests de integración de los
// repositorios. Requieren un PostgreSQL con el esquema migrado. Si la base no
// está disponible, los tests se saltan (no fallan) para no romper entornos sin DB.
//
// DSN configurable por DATABASE_URL; por defecto usa el Postgres de desarrollo.

const defaultTestDSN = "postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable"

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}

	pool, err := pkgpostgres.New(ctx, pkgpostgres.DefaultConfig(dsn))
	if err == nil {
		if pingErr := pool.Ping(ctx); pingErr == nil {
			testPool = pool
		}
	}

	code := m.Run()

	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

// requireDB retorna el pool o salta el test si no hay base disponible.
func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testPool == nil {
		t.Skip("postgres no disponible: se omiten los tests de integración")
	}
	return testPool
}

// seedTenant inserta un tenant nuevo y programa la limpieza de su subárbol.
func seedTenant(t *testing.T, pool *pgxpool.Pool) *domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(domain.NewTenantID(), "Test Tenant")
	if err != nil {
		t.Fatalf("build tenant: %v", err)
	}
	tenant.PullEvents()

	if err := NewTenantRepository(pool).Save(context.Background(), tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	cleanupTenant(t, pool, tenant.ID())
	return tenant
}

// seedUser inserta un usuario activo en el tenant dado.
func seedUser(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID, rawEmail string) *domain.User {
	t.Helper()
	email, err := domain.NewEmail(rawEmail)
	if err != nil {
		t.Fatalf("build email: %v", err)
	}
	u, err := domain.NewUser(domain.NewUserID(), tenantID, email, "argon2:hash")
	if err != nil {
		t.Fatalf("build user: %v", err)
	}
	u.PullEvents()
	if err := NewUserRepository(pool).Save(context.Background(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// cleanupTenant borra el tenant y todo lo que cuelga de él tras el test.
func cleanupTenant(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		id := tenantID.String()
		// users cascada refresh_tokens + memberships; api_keys y tenant aparte.
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, id)
	})
}

// timesClose compara dos instantes con tolerancia (Postgres trunca a microsegundos).
func timesClose(t *testing.T, got, want time.Time) {
	t.Helper()
	if d := got.Sub(want); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("timestamps differ: got %v want %v (Δ=%v)", got, want, d)
	}
}
