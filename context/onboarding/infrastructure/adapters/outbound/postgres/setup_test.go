package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// Tests de integración del repositorio de onboarding. Requieren PostgreSQL con
// el esquema migrado; si no está disponible, se saltan. DSN por DATABASE_URL.

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
		t.Skip("postgres no disponible: se omiten los tests de integración de onboarding")
	}
	return testPool
}

func testTenantID(t *testing.T) domain.TenantID {
	t.Helper()
	id, err := domain.ParseTenantID(uuidNew())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return id
}

func completeInfo() domain.BusinessInfo {
	return domain.BusinessInfo{
		LegalName:        "Acme Integration",
		TaxID:            domain.TaxID("20304050607"),
		BusinessCategory: domain.CategoryRetail,
		Address:          domain.Address{Street: "Calle 1", City: "CABA", State: "BA", Country: "AR", PostalCode: "1000"},
		Website:          "acme.example",
		PhoneNumber:      "+540111",
	}
}

// newPendingApp construye una aplicación pending (sin colecciones aún).
func newPendingApp(t *testing.T, tenantID domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a, err := domain.NewOnboardingApplication(domain.NewApplicationID(), tenantID, completeInfo())
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	a.PullEvents()
	return a
}

// addCompleteness agrega documento, persona y cuenta bancaria al agregado.
func addCompleteness(t *testing.T, a *domain.OnboardingApplication) {
	t.Helper()
	if err := a.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypeIDCard, "s3://ref")); err != nil {
		t.Fatalf("add doc: %v", err)
	}
	if err := a.AddPerson(domain.NewPerson(domain.NewPersonID(), "Owner", domain.RoleOwner, "DNI", "123", "AR")); err != nil {
		t.Fatalf("add person: %v", err)
	}
	if err := a.SetBankAccount(domain.NewBankAccount(domain.NewBankAccountID(), domain.BankAccountCBU, "0011", "Banco", "Acme", "ARS")); err != nil {
		t.Fatalf("set bank: %v", err)
	}
}

// cleanupTenant borra el subárbol de onboarding del tenant tras el test.
func cleanupTenant(t *testing.T, pool *pgxpool.Pool, tenantID domain.TenantID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		id := tenantID.String()
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_bank_accounts WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_persons WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_documents WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_applications WHERE tenant_id=$1`, id)
	})
}

func timesClose(t *testing.T, got, want time.Time) {
	t.Helper()
	if d := got.Sub(want); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("timestamps differ: got %v want %v", got, want)
	}
}

func uuidNew() string { return domain.NewApplicationID().String() }
