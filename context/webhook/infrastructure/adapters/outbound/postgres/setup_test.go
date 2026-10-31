package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// Tests de integración de los repositorios de webhook. Requieren PostgreSQL
// con el esquema migrado; si no está disponible, se saltan. DSN por
// DATABASE_URL. Los tests con pgxmock de repo_test.go corren siempre.

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
		t.Skip("postgres no disponible: se omiten los tests de integración de webhook")
	}
	return testPool
}

// seedEndpoint persiste (con el pool admin) un endpoint activo de un tenant
// nuevo y registra la limpieza de todo lo que cuelga de ese tenant.
func seedEndpoint(t *testing.T) *domain.WebhookEndpoint {
	t.Helper()
	pool := requireDB(t)
	tenantID := domain.TenantID(domain.NewEndpointID().String())
	ep, err := domain.NewWebhookEndpoint(domain.NewEndpointID(), tenantID, "https://merchant.example.com/hooks",
		"whsec_test", []string{"payment.captured"}, "integration")
	if err != nil {
		t.Fatalf("new endpoint: %v", err)
	}
	if err := NewEndpointRepository(pool).Save(context.Background(), ep); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_delivery_attempts WHERE delivery_id IN
			(SELECT id FROM webhook_deliveries WHERE tenant_id=$1)`, tenantID.String())
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE tenant_id=$1`, tenantID.String())
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE tenant_id=$1`, tenantID.String())
	})
	return ep
}

// seedDelivery persiste una delivery pending del endpoint.
func seedDelivery(t *testing.T, ep *domain.WebhookEndpoint) *domain.WebhookDelivery {
	t.Helper()
	d := domain.NewWebhookDelivery(domain.NewDeliveryID(), ep.ID(), ep.TenantID(), "payment.captured.v1",
		domain.NewDeliveryID().String(), json.RawMessage(`{"event_type":"payment.captured.v1"}`))
	if err := NewDeliveryRepository(requireDB(t)).Save(context.Background(), d); err != nil {
		t.Fatalf("save delivery: %v", err)
	}
	return d
}
