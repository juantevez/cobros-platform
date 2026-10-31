package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	onbdomain "github.com/juantevez/cobros-platform/context/onboarding/domain"
	onbpg "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/context/payout/application"
	"github.com/juantevez/cobros-platform/context/payout/domain"
	"github.com/juantevez/cobros-platform/context/payout/infrastructure/adapters/outbound/onboarding"
	"github.com/juantevez/cobros-platform/context/payout/infrastructure/adapters/outbound/transfer/mock"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// de payouts y de las tablas de Onboarding se aplican de verdad.
const rlsProbeRole = "payout_rls_probe"

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
		GRANT SELECT, INSERT, UPDATE ON payouts TO `+rlsProbeRole+`;
		GRANT SELECT ON onboarding_applications, onboarding_bank_accounts TO `+rlsProbeRole); err != nil {
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

func asTenant(id domain.TenantID) context.Context {
	return pkgpostgres.WithTenantID(context.Background(), id.String())
}

func TestRLS_Payouts(t *testing.T) {
	rls := requireRLSPool(t)
	repo := NewPayoutRepository(rls)
	a, b := savedPayout(t), savedPayout(t)

	if got, err := repo.FindByID(asTenant(a.TenantID()), a.ID()); err != nil || got.ID() != a.ID() {
		t.Fatalf("FindByID as A = %v, %v", got, err)
	}
	for name, ctx := range map[string]context.Context{"other tenant": asTenant(b.TenantID()), "no tenant": context.Background()} {
		if _, err := repo.FindByID(ctx, a.ID()); !errors.Is(err, domain.ErrPayoutNotFound) {
			t.Errorf("FindByID(A) with %s = %v, want ErrPayoutNotFound", name, err)
		}
	}
	list, err := repo.ListByTenant(context.Background(), a.TenantID(), 50)
	if err != nil || len(list) != 1 || list[0].ID() != a.ID() {
		t.Fatalf("ListByTenant(A) = %d, %v", len(list), err)
	}

	// El recuperador lista como operador, cross-tenant, pero no escribe.
	_ = a.MarkProcessing()
	if err := repo.Update(context.Background(), a, domain.StatusInitiated); err != nil {
		t.Fatalf("update under RLS: %v", err)
	}
	unresolved, err := repo.ListUnresolved(context.Background(), time.Now().UTC().Add(time.Minute), 1000)
	if err != nil {
		t.Fatalf("list unresolved as operator: %v", err)
	}
	found := false
	for _, p := range unresolved {
		found = found || p.ID() == a.ID()
	}
	if !found {
		t.Error("operator listing should include A's processing payout")
	}
	var affected int64
	err = pkgpostgres.RunWithAppTenant(pkgpostgres.WithPlatformOperator(context.Background()), rls, func(ctx context.Context) error {
		tag, err := pkgpostgres.ConnFromContext(ctx, rls).Exec(ctx, `UPDATE payouts SET failure_reason='x' WHERE id=$1`, a.ID().String())
		affected = tag.RowsAffected()
		return err
	})
	if err != nil || affected != 0 {
		t.Errorf("operator update affected %d rows (%v), want 0", affected, err)
	}
}

type openGate struct{}

func (openGate) EnsurePayoutsAllowed(context.Context, domain.TenantID) error { return nil }

type fixedBalance struct{ amount int64 }

func (b fixedBalance) GetAvailableBalance(context.Context, domain.TenantID, string) (int64, error) {
	return b.amount, nil
}

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, ...domain.Event) error { return nil }

type wallClock struct{ offset time.Duration }

func (c wallClock) Now() time.Time { return time.Now().UTC().Add(c.offset) }

// Flujos con un rol sin BYPASSRLS: desembolsar a la cuenta aprobada en
// Onboarding (provider y repo en la sesión del tenant), y el recuperador (sin
// tenant, como en el worker) cerrando un payout unknown.
func TestRLS_PayoutFlows(t *testing.T) {
	rls := requireRLSPool(t)
	admin := requireDB(t)
	ctx := context.Background()

	merchant, _ := onbdomain.ParseTenantID(onbdomain.NewApplicationID().String())
	t.Cleanup(func() {
		for _, table := range []string{"payouts", "onboarding_bank_accounts", "onboarding_persons", "onboarding_documents", "onboarding_applications"} {
			_, _ = admin.Exec(context.Background(), `DELETE FROM `+table+` WHERE tenant_id=$1`, merchant.String())
		}
	})
	onbRepo := onbpg.NewApplicationRepository(admin)
	app, _ := onbdomain.NewOnboardingApplication(onbdomain.NewApplicationID(), merchant, onbdomain.BusinessInfo{
		LegalName: "Acme", TaxID: onbdomain.TaxID("20304050607"), BusinessCategory: onbdomain.CategoryRetail,
		Address: onbdomain.Address{Street: "Calle 1", City: "CABA", State: "BA", Country: "AR", PostalCode: "1000"},
	})
	if err := onbRepo.Save(ctx, app); err != nil {
		t.Fatalf("save application: %v", err)
	}
	_ = app.AddDocument(onbdomain.NewBusinessDocument(onbdomain.NewDocumentID(), onbdomain.DocTypeIDCard, "s3://ref"))
	_ = app.AddPerson(onbdomain.NewPerson(onbdomain.NewPersonID(), "Owner", onbdomain.RoleOwner, "DNI", "123", "AR"))
	_ = app.SetBankAccount(onbdomain.NewBankAccount(onbdomain.NewBankAccountID(), onbdomain.BankAccountCBU, "0011223344556677889900", "Banco", "Acme", "ARS"))
	_ = app.SubmitForReview()
	if err := app.Approve("ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := onbRepo.Update(ctx, app); err != nil {
		t.Fatalf("update application: %v", err)
	}

	repo, tx := NewPayoutRepository(rls), pkgpostgres.NewTxManager(rls)
	uc := application.NewInitiatePayoutUseCase(openGate{}, repo, fixedBalance{10_000}, onboarding.NewBankAccountProvider(rls),
		mock.New(), tx, noopPublisher{})
	tenant := domain.TenantID(merchant.String())
	res, err := uc.Execute(asTenant(tenant), application.InitiatePayoutCmd{TenantID: tenant.String(), Amount: 5000, Currency: "ARS", IdempotencyKey: "rls-1"})
	if err != nil || res.Status != "confirmed" {
		t.Fatalf("payout under RLS = %+v, %v", res, err)
	}

	// Un payout unknown viejo: el recuperador (una hora "en el futuro", el
	// mock sin registro) lo falla escribiendo con el tenant del payout.
	stuck := savedPayout(t)
	_ = stuck.MarkProcessing()
	adminRepo := NewPayoutRepository(admin)
	_ = adminRepo.Update(ctx, stuck, domain.StatusInitiated)
	_ = stuck.MarkUnknown("timeout")
	_ = adminRepo.Update(ctx, stuck, domain.StatusProcessing)

	recoverer := application.NewPayoutRecoverer(repo, mock.New(), tx, noopPublisher{}, wallClock{offset: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = recoverer.Start(rctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := adminRepo.FindByID(ctx, stuck.ID())
		if err == nil && p.Status() == domain.StatusFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("unknown payout not resolved under RLS: %v, %v", p.Status(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
