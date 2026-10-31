package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/fees"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/psp"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/risk"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// de payments se aplican de verdad (el rol de la app, owner/superuser, las
// saltea). NOLOGIN: solo se usa vía SET ROLE desde la conexión de tests.
const rlsProbeRole = "payment_rls_probe"

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
		GRANT SELECT, INSERT, UPDATE ON payments TO `+rlsProbeRole); err != nil {
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

func TestRLS_Payments(t *testing.T) {
	rls := requireRLSPool(t)
	repo := NewPaymentRepository(rls)
	a, b := savedPayment(t), savedPayment(t)

	if got, err := repo.FindByID(asTenant(a.TenantID()), a.ID()); err != nil || got.ID() != a.ID() {
		t.Fatalf("FindByID as A = %v, %v", got, err)
	}
	for name, ctx := range map[string]context.Context{"other tenant": asTenant(b.TenantID()), "no tenant": context.Background()} {
		if _, err := repo.FindByID(ctx, a.ID()); !errors.Is(err, domain.ErrPaymentNotFound) {
			t.Errorf("FindByID(A) with %s = %v, want ErrPaymentNotFound", name, err)
		}
	}
	if _, err := repo.FindByIdempotencyKey(context.Background(), b.TenantID(), a.IdempotencyKey()); !errors.Is(err, domain.ErrPaymentNotFound) {
		t.Errorf("B found A's key: %v", err)
	}

	// Escribir un pago ajeno desde la sesión de otro tenant no toca la fila.
	foreign := domain.ReconstitutePayment(a.ID(), b.TenantID(), a.IdempotencyKey(), "",
		a.Amount(), a.PlatformFee(), a.PSPFee(), a.PayerInfo(), a.Method(), "", "",
		domain.StatusProcessing, "", nil, nil, nil, nil, a.CreatedAt(), time.Now().UTC(), "", nil, "")
	if err := repo.Update(context.Background(), foreign, domain.StatusInitiated); !errors.Is(err, domain.ErrPaymentChanged) {
		t.Errorf("update of A through B's session = %v, want ErrPaymentChanged (row not visible)", err)
	}

	// El recuperador lista como operador, cross-tenant, pero no escribe.
	_ = a.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))
	if ok, err := repo.ClaimProcessing(context.Background(), a); !ok || err != nil {
		t.Fatalf("claim under RLS = %v, %v", ok, err)
	}
	stuck, err := repo.ListStuckProcessing(context.Background(), time.Now().UTC().Add(time.Minute), 1000)
	if err != nil {
		t.Fatalf("list stuck as operator: %v", err)
	}
	found := false
	for _, p := range stuck {
		found = found || p.ID() == a.ID()
	}
	if !found {
		t.Error("operator listing should include A's processing payment")
	}
	var affected int64
	err = pkgpostgres.RunWithAppTenant(pkgpostgres.WithPlatformOperator(context.Background()), rls, func(ctx context.Context) error {
		tag, err := pkgpostgres.ConnFromContext(ctx, rls).Exec(ctx, `UPDATE payments SET failure_reason='x' WHERE id=$1`, a.ID().String())
		affected = tag.RowsAffected()
		return err
	})
	if err != nil || affected != 0 {
		t.Errorf("operator update affected %d rows (%v), want 0", affected, err)
	}
}

type openGate struct{}

func (openGate) EnsureCanProcessPayments(context.Context, domain.TenantID) error { return nil }

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, ...domain.Event) error { return nil }

type wallClock struct{ offset time.Duration }

func (c wallClock) Now() time.Time { return time.Now().UTC().Add(c.offset) }

// Flujos completos con un rol sin BYPASSRLS: cobrar y reembolsar con el
// tenant del request; un TIMEOUT deja el pago en processing y el recuperador
// (sin tenant, como en el worker) lo resuelve.
func TestRLS_PaymentFlows(t *testing.T) {
	rls := requireRLSPool(t)
	admin := requireDB(t)
	repo, tx := NewPaymentRepository(rls), pkgpostgres.NewTxManager(rls)
	merchant := domain.TenantID(uuid.NewString())
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM payments WHERE tenant_id=$1`, merchant.String()) })
	process := application.NewProcessPaymentUseCase(repo, openGate{}, psp.NewRouter(), risk.NewPermissiveEvaluator(),
		fees.NewFixedRateCalculator(300), tx, noopPublisher{})
	cmd := func(key, token string) application.ProcessPaymentCmd {
		return application.ProcessPaymentCmd{TenantID: merchant.String(), IdempotencyKey: key, Amount: 10_000,
			Currency: "ARS", PaymentMethod: "card", PaymentToken: token}
	}

	captured, err := process.Execute(asTenant(merchant), cmd("order-ok", "tok"))
	if err != nil || captured.Status != "captured" {
		t.Fatalf("charge under RLS = %+v, %v", captured, err)
	}
	refund, err := application.NewRefundPaymentUseCase(repo, psp.NewRouter(), tx, noopPublisher{}).
		Execute(asTenant(merchant), application.RefundPaymentCmd{TenantID: merchant.String(), PaymentID: captured.PaymentID})
	if err != nil || refund.Status != "refunded" || refund.PSPReference == "" {
		t.Fatalf("refund under RLS = %+v, %v", refund, err)
	}

	pending, err := process.Execute(asTenant(merchant), cmd("order-timeout", "TIMEOUT"))
	if !errors.Is(err, domain.ErrPaymentPending) || pending.Status != "processing" {
		t.Fatalf("timeout = %+v, %v; want processing", pending, err)
	}
	// El recuperador corre una hora "en el futuro": pasó stuckAfter y
	// notFoundAfter, y el mock no tiene registro → failed.
	recoverer := application.NewProcessingRecoverer(repo, psp.NewRouter(), tx, noopPublisher{}, wallClock{offset: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = recoverer.Start(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	adminRepo := NewPaymentRepository(admin)
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := adminRepo.FindByID(context.Background(), domain.PaymentID(pending.PaymentID))
		if err == nil && p.Status() == domain.StatusFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stuck payment not resolved under RLS: %v, %v", p.Status(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
