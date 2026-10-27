package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// de onboarding_* se aplican de verdad (el rol de la app, owner/superuser,
// las saltea). NOLOGIN: solo se usa vía SET ROLE desde la conexión de tests.
const rlsProbeRole = "onboarding_rls_probe"

// requireRLSPool retorna un pool cuyas conexiones corren como rlsProbeRole.
func requireRLSPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := requireDB(t)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, `
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+rlsProbeRole+`') THEN
				CREATE ROLE `+rlsProbeRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS;
			END IF;
		END $$;
		GRANT SELECT, INSERT, UPDATE ON onboarding_applications, onboarding_documents,
			onboarding_persons, onboarding_bank_accounts TO `+rlsProbeRole); err != nil {
		t.Skipf("no se pudo preparar el rol RLS de prueba: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+rlsProbeRole)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("rls pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedApp persiste una solicitud con el pool admin (sin RLS).
func seedApp(t *testing.T) *domain.OnboardingApplication {
	t.Helper()
	admin := requireDB(t)
	tenantID := testTenantID(t)
	cleanupTenant(t, admin, tenantID)
	app := newPendingApp(t, tenantID)
	addCompleteness(t, app)
	repo := NewApplicationRepository(admin)
	if err := repo.Save(context.Background(), app); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.Update(context.Background(), app); err != nil {
		t.Fatalf("update collections: %v", err)
	}
	return app
}

func TestRLS_MerchantReadsOwnApplicationOutsideTx(t *testing.T) {
	repo := NewApplicationRepository(requireRLSPool(t))
	app := seedApp(t)

	// Sin tx ni tenant en el contexto: el repo fija app.current_tenant.
	got, err := repo.FindByTenantID(context.Background(), app.TenantID())
	if err != nil {
		t.Fatalf("find by tenant under RLS: %v", err)
	}
	if got.ID() != app.ID() || len(got.Documents()) != 1 || len(got.Persons()) != 1 || got.BankAccount() == nil {
		t.Errorf("application or relations not visible under RLS: %+v", got)
	}
}

func TestRLS_OtherTenantCannotReadByID(t *testing.T) {
	repo := NewApplicationRepository(requireRLSPool(t))
	app := seedApp(t)

	ctx := pkgpostgres.WithTenantID(context.Background(), testTenantID(t).String())
	if _, err := repo.FindByID(ctx, app.ID()); !errors.Is(err, domain.ErrApplicationNotFound) {
		t.Fatalf("other tenant must not see the application, got %v", err)
	}
	if _, err := repo.FindByID(context.Background(), app.ID()); !errors.Is(err, domain.ErrApplicationNotFound) {
		t.Fatalf("no tenant/operator must not see the application, got %v", err)
	}
}

func TestRLS_PlatformOperatorReviewsOtherTenant(t *testing.T) {
	pool := requireRLSPool(t)
	repo := NewApplicationRepository(pool)
	txm := pkgpostgres.NewTxManager(pool)
	app := seedApp(t)

	// El operador tiene su propio tenant en el JWT, distinto al de la solicitud.
	ctx := pkgpostgres.WithTenantID(context.Background(), testTenantID(t).String())
	ctx = pkgpostgres.WithPlatformOperator(ctx)

	got, err := repo.FindByID(ctx, app.ID())
	if err != nil {
		t.Fatalf("operator find by id under RLS: %v", err)
	}
	if err := got.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := got.Approve("ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := txm.RunInTx(ctx, func(ctx context.Context) error {
		return repo.Update(ctx, got)
	}); err != nil {
		t.Fatalf("operator update under RLS: %v", err)
	}

	after, err := NewApplicationRepository(requireDB(t)).FindByID(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status() != domain.StatusApproved {
		t.Errorf("status = %s, want approved", after.Status())
	}
}

func TestApplicationRepo_OptimisticLock(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()
	app := seedApp(t)

	// Dos lectores concurrentes de la misma versión.
	first, err := repo.FindByID(ctx, app.ID())
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	second, err := repo.FindByID(ctx, app.ID())
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}

	if err := first.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := repo.Update(ctx, first); err != nil {
		t.Fatalf("first update: %v", err)
	}

	if err := second.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypePassport, "s3://late")); err != nil {
		t.Fatalf("add doc: %v", err)
	}
	if err := repo.Update(ctx, second); !errors.Is(err, domain.ErrConcurrentModification) {
		t.Fatalf("stale update must fail with ErrConcurrentModification, got %v", err)
	}

	after, err := repo.FindByID(ctx, app.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status() != domain.StatusInReview || len(after.Documents()) != 1 {
		t.Errorf("stale write leaked: status=%s docs=%d", after.Status(), len(after.Documents()))
	}
	if after.Version() != first.Version() {
		t.Errorf("version = %d, want %d", after.Version(), first.Version())
	}
}
