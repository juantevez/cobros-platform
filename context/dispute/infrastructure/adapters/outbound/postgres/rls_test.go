package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// se aplican de verdad (el rol de la app, owner/superuser, las saltea).
// NOLOGIN: solo se usa vía SET ROLE desde la conexión de tests.
const rlsProbeRole = "dispute_rls_probe"

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
		GRANT SELECT, INSERT, UPDATE ON disputes, dispute_evidence TO `+rlsProbeRole+`;
		GRANT SELECT ON payments TO `+rlsProbeRole); err != nil {
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

// asOperator es el contexto de una ruta de operador: el tenant del JWT del
// soporte (que no es el de la disputa) más la marca de operador.
func asOperator() context.Context {
	return pkgpostgres.WithPlatformOperator(pkgpostgres.WithTenantID(context.Background(), uuid.NewString()))
}

// countIn cuenta filas de una tabla con la sesión RLS de ctx.
func countIn(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) int {
	t.Helper()
	var n int
	if tid, ok := pkgpostgres.TenantIDFromContext(ctx); ok {
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tid)
	}
	err := pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return pkgpostgres.ConnFromContext(ctx, pool).QueryRow(ctx, sql, args...).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRLS_Disputes(t *testing.T) {
	rls := requireRLSPool(t)
	a, b := seedDispute(t), seedDispute(t)
	repo := NewDisputeRepository(rls)

	if got, err := repo.FindByID(asTenant(a.TenantID()), a.ID()); err != nil || got.ID() != a.ID() {
		t.Fatalf("FindByID as A = %v, %v", got, err)
	}
	for name, ctx := range map[string]context.Context{"other tenant": asTenant(b.TenantID()), "no tenant": context.Background()} {
		if _, err := repo.FindByID(ctx, a.ID()); !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Errorf("FindByID(A) with %s = %v, want ErrDisputeNotFound", name, err)
		}
	}
	// El operador lee cualquiera (resolver, abrir con chequeo de duplicado).
	if got, err := repo.FindByID(asOperator(), b.ID()); err != nil || got.ID() != b.ID() {
		t.Fatalf("FindByID as operator = %v, %v", got, err)
	}
	if got, err := repo.FindByPaymentID(asOperator(), b.PaymentID()); err != nil || got.ID() != b.ID() {
		t.Fatalf("FindByPaymentID as operator = %v, %v", got, err)
	}

	list, err := repo.ListByTenant(context.Background(), a.TenantID(), "", 50)
	if err != nil || len(list) != 1 || list[0].ID() != a.ID() {
		t.Fatalf("ListByTenant(A) = %d, %v; want only A's", len(list), err)
	}
	overdue, err := repo.ListOverdue(context.Background(), time.Now().Add(48*time.Hour), 1000)
	if err != nil {
		t.Fatalf("ListOverdue as operator: %v", err)
	}
	seen := map[domain.DisputeID]bool{}
	for _, d := range overdue {
		seen[d.ID()] = true
	}
	if !seen[a.ID()] || !seen[b.ID()] {
		t.Error("operator listing should include both tenants")
	}

	// Evidencia: la de A se escribe con su tenant y no la ve B.
	d, _ := repo.FindByID(asTenant(a.TenantID()), a.ID())
	ev, _ := domain.NewEvidence(domain.NewEvidenceID(), "receipt", "s3://r", "", time.Now())
	if err := d.Contest([]domain.Evidence{ev}, "prueba", time.Now()); err != nil {
		t.Fatalf("contest: %v", err)
	}
	if err := repo.Update(asTenant(a.TenantID()), d); err != nil {
		t.Fatalf("update under RLS: %v", err)
	}
	if got, _ := repo.FindByID(asTenant(a.TenantID()), a.ID()); len(got.Evidence()) != 1 {
		t.Errorf("evidence as A = %d, want 1", len(got.Evidence()))
	}
	if n := countIn(t, rls, asTenant(b.TenantID()), `SELECT count(*) FROM dispute_evidence WHERE dispute_id=$1`, a.ID().String()); n != 0 {
		t.Errorf("evidence of A seen by B = %d", n)
	}

	// El operador solo lee: no puede modificar disputas.
	opCtx := pkgpostgres.WithPlatformOperator(context.Background())
	var affected int64
	err = pkgpostgres.RunWithAppTenant(opCtx, rls, func(ctx context.Context) error {
		tag, err := pkgpostgres.ConnFromContext(ctx, rls).Exec(ctx, `UPDATE disputes SET resolved_note='x' WHERE id=$1`, b.ID().String())
		affected = tag.RowsAffected()
		return err
	})
	if err != nil || affected != 0 {
		t.Errorf("operator update affected %d rows (%v), want 0", affected, err)
	}
}

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, ...domain.Event) error { return nil }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

// Flujo completo con un rol sin BYPASSRLS: el operador abre la disputa sobre
// el pago de un comercio (con su propio tenant en el JWT), el comercio la
// contesta y el operador la resuelve.
func TestRLS_OperatorOpensAndResolves(t *testing.T) {
	rls := requireRLSPool(t)
	admin := requireDB(t)
	ctx := context.Background()
	merchant := domain.TenantID(uuid.NewString())
	paymentID := uuid.NewString()
	now := time.Now().UTC()
	if _, err := admin.Exec(ctx, `
		INSERT INTO payments (id, tenant_id, idempotency_key, amount, currency, payment_method, status, created_at, updated_at)
		VALUES ($1, $2, $3, 5000, 'ARS', 'card', 'captured', $4, $4)`, paymentID, merchant.String(), uuid.NewString(), now); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM dispute_evidence WHERE tenant_id=$1`, merchant.String())
		_, _ = admin.Exec(ctx, `DELETE FROM disputes WHERE tenant_id=$1`, merchant.String())
		_, _ = admin.Exec(ctx, `DELETE FROM payments WHERE id=$1`, paymentID)
	})

	repo, tx := NewDisputeRepository(rls), pkgpostgres.NewTxManager(rls)
	opened, err := application.NewOpenDisputeUseCase(repo, NewPaymentReader(rls), tx, noopPublisher{}, wallClock{}).
		Execute(asOperator(), application.OpenDisputeCmd{PaymentID: paymentID, Amount: 5000, Currency: "ARS", Reason: "fraudulent"})
	if err != nil {
		t.Fatalf("open as operator under RLS: %v", err)
	}

	evidence := []application.EvidenceInput{{EvidenceType: "receipt", Reference: "s3://factura"}}
	if err := application.NewContestDisputeUseCase(repo, tx, wallClock{}).Execute(asTenant(merchant),
		application.ContestDisputeCmd{TenantID: merchant.String(), DisputeID: opened.DisputeID, Evidence: evidence}); err != nil {
		t.Fatalf("contest as merchant under RLS: %v", err)
	}

	if err := application.NewResolveDisputeUseCase(repo, tx, noopPublisher{}, wallClock{}).Execute(asOperator(),
		application.ResolveDisputeCmd{DisputeID: opened.DisputeID, Outcome: "won"}); err != nil {
		t.Fatalf("resolve as operator under RLS: %v", err)
	}

	got, err := NewDisputeRepository(admin).FindByID(ctx, domain.DisputeID(opened.DisputeID))
	if err != nil || got.TenantID() != merchant || got.Status() != domain.StatusWon || len(got.Evidence()) != 1 {
		t.Fatalf("dispute = %+v, %v; want merchant's, won, 1 evidence", got, err)
	}
}
