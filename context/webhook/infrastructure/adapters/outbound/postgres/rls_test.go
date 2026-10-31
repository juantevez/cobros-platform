package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/webhook/application"
	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// de webhook_* se aplican de verdad (el rol de la app, owner/superuser, las
// saltea). NOLOGIN: solo se usa vía SET ROLE desde la conexión de tests.
const rlsProbeRole = "webhook_rls_probe"

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
		GRANT SELECT, INSERT, UPDATE ON webhook_endpoints, webhook_deliveries,
			webhook_delivery_attempts TO `+rlsProbeRole); err != nil {
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

func asTenant(tenantID domain.TenantID) context.Context {
	return pkgpostgres.WithTenantID(context.Background(), tenantID.String())
}

func TestRLS_Endpoints(t *testing.T) {
	rls := requireRLSPool(t)
	a, b := seedEndpoint(t), seedEndpoint(t)
	repo := NewEndpointRepository(rls)

	list, err := repo.FindByTenant(context.Background(), a.TenantID())
	if err != nil || len(list) != 1 || list[0].ID() != a.ID() {
		t.Fatalf("FindByTenant(A) = %d endpoints, %v; want only A's", len(list), err)
	}
	active, err := repo.FindActiveByTenantAndEvent(context.Background(), a.TenantID(), "payment.captured.v1")
	if err != nil || len(active) != 1 {
		t.Fatalf("FindActiveByTenantAndEvent(A) = %d, %v", len(active), err)
	}
	if got, err := repo.FindByID(asTenant(a.TenantID()), a.ID()); err != nil || got.ID() != a.ID() {
		t.Fatalf("FindByID as A = %v, %v", got, err)
	}
	// Por ID con otro tenant, o sin tenant, no se ve.
	for name, ctx := range map[string]context.Context{"other tenant": asTenant(b.TenantID()), "no tenant": context.Background()} {
		if _, err := repo.FindByID(ctx, a.ID()); !errors.Is(err, domain.ErrEndpointNotFound) {
			t.Errorf("FindByID(A) with %s = %v, want ErrEndpointNotFound", name, err)
		}
	}

	// Desactivar desde otro tenant no toca la fila.
	foreign := domain.ReconstituteWebhookEndpoint(a.ID(), b.TenantID(), a.URL(), "", "", "", a.Events(), false, a.CreatedAt(), time.Now().UTC())
	if err := repo.Update(context.Background(), foreign); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, _ := NewEndpointRepository(requireDB(t)).FindByID(context.Background(), a.ID()); !got.Active() {
		t.Error("endpoint of A was deactivated through tenant B's session")
	}
}

func TestRLS_DeliveriesAndAttempts(t *testing.T) {
	rls := requireRLSPool(t)
	a, b := seedEndpoint(t), seedEndpoint(t)
	repo := NewDeliveryRepository(rls)

	d := domain.NewWebhookDelivery(domain.NewDeliveryID(), a.ID(), a.TenantID(), "payment.captured.v1",
		domain.NewDeliveryID().String(), json.RawMessage(`{}`))
	if err := repo.Save(context.Background(), d); err != nil {
		t.Fatalf("save under RLS: %v", err)
	}
	other := seedDelivery(t, b)

	ctxA := asTenant(a.TenantID())
	if _, err := repo.FindByEventAndEndpoint(ctxA, a.ID(), d.EventID()); err != nil {
		t.Fatalf("FindByEventAndEndpoint as A: %v", err)
	}
	if _, err := repo.FindByEventAndEndpoint(asTenant(b.TenantID()), a.ID(), d.EventID()); !errors.Is(err, domain.ErrDeliveryNotFound) {
		t.Errorf("A's delivery seen by B: %v", err)
	}

	// Reclamo sin tenant: no ve la fila.
	now := time.Now().UTC()
	if ok, _ := repo.Claim(context.Background(), d.ID(), 0, now, now.Add(time.Minute)); ok {
		t.Error("claim without tenant should not see the delivery")
	}
	if ok, err := repo.Claim(ctxA, d.ID(), 0, now, now.Add(time.Minute)); !ok || err != nil {
		t.Fatalf("claim as A = %v, %v", ok, err)
	}
	att := domain.NewDeliveryAttempt(1, 200, "ok", "", 5)
	_ = d.RecordAttempt(att, att.AttemptedAt())
	if err := repo.Update(context.Background(), d, 0); err != nil {
		t.Fatalf("update under RLS: %v", err)
	}
	got, err := repo.FindByID(ctxA, d.ID())
	if err != nil || got.Status() != domain.StatusDelivered || len(got.Attempts()) != 1 {
		t.Fatalf("FindByID as A = %v, %v", got, err)
	}

	// Los intentos de A no se ven desde B.
	var n int
	err = pkgpostgres.RunWithAppTenant(pkgpostgres.WithAppTenant(context.Background(), rlsTenantKey, b.TenantID().String()), rls,
		func(ctx context.Context) error {
			return pkgpostgres.ConnFromContext(ctx, rls).QueryRow(ctx,
				`SELECT count(*) FROM webhook_delivery_attempts WHERE delivery_id=$1`, d.ID().String()).Scan(&n)
		})
	if err != nil || n != 0 {
		t.Errorf("attempts of A seen by B = %d, %v", n, err)
	}

	// El poller lista como operador, cross-tenant.
	due, err := repo.ListDueForRetry(context.Background(), time.Now().UTC().Add(time.Second), 1000)
	if err != nil {
		t.Fatalf("list due as operator: %v", err)
	}
	found := false
	for _, x := range due {
		found = found || x.ID() == other.ID()
	}
	if !found {
		t.Error("operator listing should include B's due delivery")
	}

	// El operador solo lee: sin tenant no puede reclamar.
	opCtx := pkgpostgres.WithPlatformOperator(context.Background())
	if ok, _ := repo.Claim(opCtx, other.ID(), 0, now, now.Add(time.Minute)); ok {
		t.Error("platform operator must not write deliveries")
	}
}

// okDispatcher responde 2xx sin red.
type okDispatcher struct{}

func (okDispatcher) Dispatch(_ context.Context, _ *domain.WebhookEndpoint, d *domain.WebhookDelivery) (domain.DeliveryAttempt, error) {
	return domain.NewDeliveryAttempt(d.AttemptCount()+1, 204, "", "", 1), nil
}

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, ...domain.Event) error { return nil }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

// Flujo completo con un rol sin BYPASSRLS: el consumer (tenant del evento en
// el contexto) crea la delivery y el poller (sin tenant) la entrega.
func TestRLS_ConsumerAndPollerDeliver(t *testing.T) {
	rls := requireRLSPool(t)
	ep := seedEndpoint(t)
	endpoints, deliveries := NewEndpointRepository(rls), NewDeliveryRepository(rls)

	eventID := domain.NewDeliveryID().String()
	err := application.NewDispatchEventUseCase(endpoints, deliveries).Execute(asTenant(ep.TenantID()), application.IncomingEvent{
		TenantID: ep.TenantID().String(), EventType: "payment.captured.v1", EventID: eventID,
		OccurredAt: time.Now().UTC(), Data: json.RawMessage(`{"amount":1}`),
	})
	if err != nil {
		t.Fatalf("dispatch event: %v", err)
	}

	retry := application.NewRetryDeliveryUseCase(endpoints, deliveries, okDispatcher{}, pkgpostgres.NewTxManager(rls), noopPublisher{}, wallClock{})
	poller := application.NewRetryPoller(retry, deliveries, 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = poller.Start(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	admin := NewDeliveryRepository(requireDB(t))
	deadline := time.Now().Add(5 * time.Second)
	for {
		d, err := admin.FindByEventAndEndpoint(context.Background(), ep.ID(), eventID)
		if err == nil && d.Status() == domain.StatusDelivered && len(d.Attempts()) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery not delivered under RLS: %v, %v", d, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
