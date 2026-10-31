package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

var errBoom = errors.New("boom")

var (
	tenantA  = domain.TenantID("11111111-1111-1111-1111-111111111111")
	testTime = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
)

func newMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock
}

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

func strPtr(s string) *string { return &s }

// ── helpers ───────────────────────────────────────────────────────────────────

func TestNullStr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty string should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("nullStr(\"x\") = %v", got)
	}
}

func TestWrapErr(t *testing.T) {
	if wrapErr("op", nil) != nil {
		t.Error("nil error should stay nil")
	}
	if err := wrapErr("op", errBoom); !errors.Is(err, errBoom) || err.Error() != "endpoint repo: op: boom" {
		t.Errorf("wrapErr = %v", err)
	}
}

func TestToTextArray(t *testing.T) {
	got := toTextArray([]string{"a.b", "c.d"})
	if !got.Valid || len(got.Dims) != 1 || got.Dims[0].Length != 2 || got.Dims[0].LowerBound != 1 {
		t.Errorf("array header = %+v", got)
	}
	want := []pgtype.Text{{String: "a.b", Valid: true}, {String: "c.d", Valid: true}}
	if !reflect.DeepEqual(got.Elements, want) {
		t.Errorf("elements = %+v", got.Elements)
	}
	if empty := toTextArray(nil); !empty.Valid || len(empty.Elements) != 0 || empty.Dims[0].Length != 0 {
		t.Errorf("empty array = %+v", empty)
	}
}

func TestStripVersion(t *testing.T) {
	cases := map[string]string{
		"payment.captured.v1": "payment.captured",
		"payment.captured.v":  "payment.captured.v",
		"payment.captured.vx": "payment.captured.vx",
		"payment.captured":    "payment.captured",
		"v1":                  "v1", // un solo segmento: no se recorta
		"":                    "",
	}
	for in, want := range cases {
		if got := stripVersion(in); got != want {
			t.Errorf("stripVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── EndpointRepository ────────────────────────────────────────────────────────

var endpointCols = []string{"id", "tenant_id", "url", "secret", "secret_hint", "events", "active", "description", "created_at", "updated_at"}

func endpointRow(id string, events ...string) []any {
	return []any{
		id, tenantA.String(), "https://merchant.example.com/hooks", "whsec_x", "...ex",
		toTextArray(events), true, "main", testTime, testTime.Add(time.Hour),
	}
}

func newEndpoint(t *testing.T) *domain.WebhookEndpoint {
	t.Helper()
	e, err := domain.NewWebhookEndpoint(domain.NewEndpointID(), tenantA, "https://merchant.example.com/hooks",
		"whsec_0123456789abcdef", []string{"payment.captured", "payout.confirmed"}, "main")
	if err != nil {
		t.Fatalf("NewWebhookEndpoint: %v", err)
	}
	return e
}

func TestEndpointRepository_Save(t *testing.T) {
	t.Run("inserts endpoint with events array", func(t *testing.T) {
		mock := newMock(t)
		e := newEndpoint(t)

		mock.ExpectExec("INSERT INTO webhook_endpoints").WithArgs(
			e.ID().String(), tenantA.String(),
			"https://merchant.example.com/hooks", "whsec_0123456789abcdef", "...cdef",
			toTextArray([]string{"payment.captured", "payout.confirmed"}),
			true, "main", e.CreatedAt(), e.UpdatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := NewEndpointRepository(mock).Save(context.Background(), e); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("INSERT INTO webhook_endpoints").WithArgs(anyArgs(10)...).WillReturnError(errBoom)

		err := NewEndpointRepository(mock).Save(context.Background(), newEndpoint(t))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "endpoint repo: save") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestEndpointRepository_Update(t *testing.T) {
	t.Run("updates active flag", func(t *testing.T) {
		mock := newMock(t)
		e := newEndpoint(t)
		e.Deactivate()

		mock.ExpectExec("UPDATE webhook_endpoints SET active=\\$2, updated_at=\\$3 WHERE id=\\$1").
			WithArgs(e.ID().String(), false, e.UpdatedAt()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := NewEndpointRepository(mock).Update(context.Background(), e); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("UPDATE webhook_endpoints").WithArgs(anyArgs(3)...).WillReturnError(errBoom)

		err := NewEndpointRepository(mock).Update(context.Background(), newEndpoint(t))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "endpoint repo: update") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestEndpointRepository_FindByID(t *testing.T) {
	id := uuid.NewString()

	t.Run("reconstitutes endpoint", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints WHERE id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(endpointCols).AddRow(endpointRow(id, "payment.captured", "payout.confirmed")...))

		e, err := NewEndpointRepository(mock).FindByID(context.Background(), domain.EndpointID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.ID().String() != id || e.TenantID() != tenantA || e.URL() != "https://merchant.example.com/hooks" ||
			e.Secret() != "whsec_x" || e.SecretHint() != "...ex" || !e.Active() || e.Description() != "main" {
			t.Errorf("fields not mapped: %+v", e)
		}
		if !reflect.DeepEqual(e.Events(), []string{"payment.captured", "payout.confirmed"}) {
			t.Errorf("events = %v", e.Events())
		}
		if !e.CreatedAt().Equal(testTime) || e.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", e.CreatedAt())
		}
	})

	t.Run("NULL array elements are dropped", func(t *testing.T) {
		mock := newMock(t)
		row := endpointRow(id)
		row[5] = pgtype.Array[pgtype.Text]{
			Elements: []pgtype.Text{{String: "a.b", Valid: true}, {Valid: false}},
			Dims:     []pgtype.ArrayDimension{{Length: 2, LowerBound: 1}},
			Valid:    true,
		}
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(id).WillReturnRows(pgxmock.NewRows(endpointCols).AddRow(row...))

		e, err := NewEndpointRepository(mock).FindByID(context.Background(), domain.EndpointID(id))
		if err != nil || !reflect.DeepEqual(e.Events(), []string{"a.b"}) {
			t.Fatalf("got %v, %v", e, err)
		}
	})

	t.Run("no rows maps to ErrEndpointNotFound", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(id).WillReturnRows(pgxmock.NewRows(endpointCols))

		_, err := NewEndpointRepository(mock).FindByID(context.Background(), domain.EndpointID(id))
		if !errors.Is(err, domain.ErrEndpointNotFound) {
			t.Fatalf("expected ErrEndpointNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(id).WillReturnError(errBoom)

		_, err := NewEndpointRepository(mock).FindByID(context.Background(), domain.EndpointID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "endpoint repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestEndpointRepository_FindByTenant(t *testing.T) {
	t.Run("lists endpoints of tenant", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY created_at DESC").WithArgs(tenantA.String()).
			WillReturnRows(pgxmock.NewRows(endpointCols).
				AddRow(endpointRow(uuid.NewString(), "a.b")...).
				AddRow(endpointRow(uuid.NewString(), "c.d")...))

		got, err := NewEndpointRepository(mock).FindByTenant(context.Background(), tenantA)
		if err != nil || len(got) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(tenantA.String()).WillReturnError(errBoom)

		_, err := NewEndpointRepository(mock).FindByTenant(context.Background(), tenantA)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "endpoint repo: list") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock := newMock(t)
		bad := endpointRow(uuid.NewString())
		bad[6] = "not-a-bool"
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(tenantA.String()).WillReturnRows(pgxmock.NewRows(endpointCols).AddRow(bad...))

		_, err := NewEndpointRepository(mock).FindByTenant(context.Background(), tenantA)
		if err == nil || !strings.Contains(err.Error(), "endpoint repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(tenantA.String()).
			WillReturnRows(pgxmock.NewRows(endpointCols).AddRow(endpointRow(uuid.NewString())...).RowError(0, errBoom))

		_, err := NewEndpointRepository(mock).FindByTenant(context.Background(), tenantA)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestEndpointRepository_FindActiveByTenantAndEvent(t *testing.T) {
	t.Run("matches versioned and unversioned event type", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE tenant_id=\\$1 AND active=true\\s+AND \\(events @> ARRAY\\[\\$2::text\\] OR events @> ARRAY\\[\\$3::text\\]\\)").
			WithArgs(tenantA.String(), "payment.captured.v1", "payment.captured").
			WillReturnRows(pgxmock.NewRows(endpointCols).AddRow(endpointRow(uuid.NewString(), "payment.captured")...))

		got, err := NewEndpointRepository(mock).FindActiveByTenantAndEvent(context.Background(), tenantA, "payment.captured.v1")
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_endpoints").WithArgs(anyArgs(3)...).WillReturnError(errBoom)

		_, err := NewEndpointRepository(mock).FindActiveByTenantAndEvent(context.Background(), tenantA, "x.y")
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "find active by event") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

// ── DeliveryRepository ────────────────────────────────────────────────────────

var (
	deliveryCols = []string{"id", "endpoint_id", "tenant_id", "event_type", "event_id", "payload", "status", "attempt_count", "next_retry_at", "created_at", "updated_at"}
	attemptCols  = []string{"id", "attempt_num", "http_status", "response_body", "error_msg", "duration_ms", "attempted_at"}
)

func deliveryRow(id string) []any {
	next := testTime.Add(30 * time.Second)
	return []any{
		id, uuid.NewString(), tenantA.String(), "payment.captured", "evt-1",
		[]byte(`{"a":1}`), "failed", 1, &next, testTime, testTime,
	}
}

func newDelivery() *domain.WebhookDelivery {
	return domain.NewWebhookDelivery(domain.NewDeliveryID(), domain.NewEndpointID(), tenantA, "payment.captured.v1", "evt-1", json.RawMessage(`{"a":1}`))
}

func TestDeliveryRepository_Save(t *testing.T) {
	t.Run("inserts delivery", func(t *testing.T) {
		mock := newMock(t)
		d := newDelivery()

		mock.ExpectExec("INSERT INTO webhook_deliveries").WithArgs(
			d.ID().String(), d.EndpointID().String(), tenantA.String(),
			"payment.captured", "evt-1", []byte(`{"a":1}`),
			"pending", 0, d.NextRetryAt(), d.CreatedAt(), d.UpdatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := NewDeliveryRepository(mock).Save(context.Background(), d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("INSERT INTO webhook_deliveries").WithArgs(anyArgs(11)...).WillReturnError(errBoom)

		err := NewDeliveryRepository(mock).Save(context.Background(), newDelivery())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "delivery repo: save") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestDeliveryRepository_Update(t *testing.T) {
	t.Run("updates state and inserts only the last attempt", func(t *testing.T) {
		mock := newMock(t)
		d := newDelivery()
		_ = d.RecordAttempt(domain.NewDeliveryAttempt(1, 500, "boom", "", 10), testTime)
		_ = d.RecordAttempt(domain.NewDeliveryAttempt(2, 0, "", "timeout", 30000), testTime)
		last := d.Attempts()[1]

		mock.ExpectExec("UPDATE webhook_deliveries SET.*claimed_until=NULL\\s+WHERE id=\\$1 AND attempt_count=\\$6").
			WithArgs(d.ID().String(), "failed", 2, d.NextRetryAt(), d.UpdatedAt(), 1).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("INSERT INTO webhook_delivery_attempts.*ON CONFLICT \\(id\\) DO NOTHING").WithArgs(
			last.ID().String(), d.ID().String(), 2, 0,
			(*string)(nil), strPtr("timeout"), int64(30000), last.AttemptedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := NewDeliveryRepository(mock).Update(context.Background(), d, 1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("without attempts only updates the delivery", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("UPDATE webhook_deliveries SET").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := NewDeliveryRepository(mock).Update(context.Background(), newDelivery(), 0); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("update error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("UPDATE webhook_deliveries").WithArgs(anyArgs(6)...).WillReturnError(errBoom)

		err := NewDeliveryRepository(mock).Update(context.Background(), newDelivery(), 0)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "delivery repo: update") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("attempt_count changed by another process", func(t *testing.T) {
		mock := newMock(t)
		d := newDelivery()
		_ = d.RecordAttempt(domain.NewDeliveryAttempt(1, 200, "ok", "", 5), testTime)
		mock.ExpectExec("UPDATE webhook_deliveries").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		err := NewDeliveryRepository(mock).Update(context.Background(), d, 0)
		if !errors.Is(err, domain.ErrDeliveryInProgress) {
			t.Fatalf("expected ErrDeliveryInProgress, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("attempt must not be inserted: %v", err)
		}
	})

	t.Run("attempt insert error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		d := newDelivery()
		_ = d.RecordAttempt(domain.NewDeliveryAttempt(1, 200, "ok", "", 5), testTime)
		mock.ExpectExec("UPDATE webhook_deliveries").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("INSERT INTO webhook_delivery_attempts").WithArgs(anyArgs(8)...).WillReturnError(errBoom)

		err := NewDeliveryRepository(mock).Update(context.Background(), d, 0)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save attempt") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestDeliveryRepository_FindByID(t *testing.T) {
	id := uuid.NewString()

	t.Run("loads delivery with attempts in order", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries d WHERE d.id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(id)...))
		mock.ExpectQuery("FROM webhook_delivery_attempts\\s+WHERE delivery_id=\\$1 ORDER BY attempt_num").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(attemptCols).
				AddRow("a1", 1, 500, "oops", "", int64(10), testTime))

		d, err := NewDeliveryRepository(mock).FindByID(context.Background(), domain.DeliveryID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ID().String() != id || d.TenantID() != tenantA || d.EventType() != "payment.captured" || string(d.Payload()) != `{"a":1}` {
			t.Error("delivery not mapped")
		}
		if d.Status() != domain.StatusFailed || d.AttemptCount() != 1 || d.NextRetryAt() == nil {
			t.Errorf("state = %q %d %v", d.Status(), d.AttemptCount(), d.NextRetryAt())
		}
		if len(d.Attempts()) != 1 {
			t.Fatalf("attempts = %d, want 1", len(d.Attempts()))
		}
		a := d.Attempts()[0]
		if a.ID() != "a1" || a.HTTPStatus() != 500 || a.ResponseBody() != "oops" || a.DurationMs() != 10 || !a.AttemptedAt().Equal(testTime) {
			t.Errorf("attempt = %+v", a)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no rows maps to ErrDeliveryNotFound", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(id).WillReturnRows(pgxmock.NewRows(deliveryCols))

		_, err := NewDeliveryRepository(mock).FindByID(context.Background(), domain.DeliveryID(id))
		if !errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected ErrDeliveryNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(id).WillReturnError(errBoom)

		_, err := NewDeliveryRepository(mock).FindByID(context.Background(), domain.DeliveryID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "delivery repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("attempts query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(id).WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(id)...))
		mock.ExpectQuery("FROM webhook_delivery_attempts").WithArgs(id).WillReturnError(errBoom)

		_, err := NewDeliveryRepository(mock).FindByID(context.Background(), domain.DeliveryID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "load attempts") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("attempt scan error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(id).WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(id)...))
		mock.ExpectQuery("FROM webhook_delivery_attempts").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(attemptCols).AddRow("a1", "x", 500, "", "", int64(1), testTime))

		_, err := NewDeliveryRepository(mock).FindByID(context.Background(), domain.DeliveryID(id))
		if err == nil || !strings.Contains(err.Error(), "scan attempt") {
			t.Fatalf("expected scan attempt error, got %v", err)
		}
	})
}

func TestDeliveryRepository_FindByEventAndEndpoint(t *testing.T) {
	endpointID := domain.NewEndpointID()

	t.Run("returns existing delivery", func(t *testing.T) {
		mock := newMock(t)
		id := uuid.NewString()
		mock.ExpectQuery("WHERE d.endpoint_id=\\$1 AND d.event_id=\\$2").WithArgs(endpointID.String(), "evt-1").
			WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(id)...))
		mock.ExpectQuery("FROM webhook_delivery_attempts").WithArgs(id).WillReturnRows(pgxmock.NewRows(attemptCols))

		d, err := NewDeliveryRepository(mock).FindByEventAndEndpoint(context.Background(), endpointID, "evt-1")
		if err != nil || d.ID().String() != id {
			t.Fatalf("got %v, %v", d, err)
		}
	})

	t.Run("missing delivery returns ErrDeliveryNotFound", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE d.endpoint_id").WithArgs(endpointID.String(), "evt-1").WillReturnRows(pgxmock.NewRows(deliveryCols))

		_, err := NewDeliveryRepository(mock).FindByEventAndEndpoint(context.Background(), endpointID, "evt-1")
		if !errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected ErrDeliveryNotFound, got %v", err)
		}
	})

	t.Run("other errors are returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE d.endpoint_id").WithArgs(endpointID.String(), "evt-1").WillReturnError(errBoom)

		_, err := NewDeliveryRepository(mock).FindByEventAndEndpoint(context.Background(), endpointID, "evt-1")
		if !errors.Is(err, errBoom) || errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestDeliveryRepository_ListByTenant(t *testing.T) {
	t.Run("lists deliveries without attempts", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE d.tenant_id=\\$1 ORDER BY d.created_at DESC LIMIT \\$2").WithArgs(tenantA.String(), 20).
			WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(uuid.NewString())...).AddRow(deliveryRow(uuid.NewString())...))

		got, err := NewDeliveryRepository(mock).ListByTenant(context.Background(), tenantA, 20)
		if err != nil || len(got) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
		if len(got[0].Attempts()) != 0 {
			t.Error("list does not load attempts")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(tenantA.String(), 20).WillReturnError(errBoom)

		_, err := NewDeliveryRepository(mock).ListByTenant(context.Background(), tenantA, 20)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "delivery repo: list") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock := newMock(t)
		bad := deliveryRow(uuid.NewString())
		bad[7] = "x"
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(tenantA.String(), 20).WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(bad...))

		_, err := NewDeliveryRepository(mock).ListByTenant(context.Background(), tenantA, 20)
		if err == nil || !strings.Contains(err.Error(), "delivery repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})
}

func TestDeliveryRepository_Claim(t *testing.T) {
	id := domain.NewDeliveryID()
	until := testTime.Add(2 * time.Minute)
	for _, tc := range []struct {
		rows int64
		want bool
	}{{1, true}, {0, false}} {
		mock := newMock(t)
		mock.ExpectExec("UPDATE webhook_deliveries SET claimed_until=\\$4\\s+WHERE id=\\$1\\s+AND status IN \\('pending','failed'\\)\\s+AND attempt_count=\\$2\\s+AND \\(claimed_until IS NULL OR claimed_until < \\$3\\)").
			WithArgs(id.String(), 3, testTime, until).
			WillReturnResult(pgxmock.NewResult("UPDATE", tc.rows))

		got, err := NewDeliveryRepository(mock).Claim(context.Background(), id, 3, testTime, until)
		if err != nil || got != tc.want {
			t.Errorf("rows %d: Claim = %v, %v; want %v", tc.rows, got, err, tc.want)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	}

	mock := newMock(t)
	mock.ExpectExec("UPDATE webhook_deliveries").WithArgs(anyArgs(4)...).WillReturnError(errBoom)
	if _, err := NewDeliveryRepository(mock).Claim(context.Background(), id, 3, testTime, until); !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "delivery repo: claim") {
		t.Errorf("expected wrapped errBoom, got %v", err)
	}
}

func TestDeliveryRepository_ListDueForRetry(t *testing.T) {
	t.Run("lists pending and failed due deliveries", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("JOIN webhook_endpoints e ON e.id = d.endpoint_id AND e.active = true\\s+WHERE d.status IN \\('pending','failed'\\)\\s+AND d.next_retry_at <= \\$1\\s+AND \\(d.claimed_until IS NULL OR d.claimed_until < \\$1\\)\\s+ORDER BY d.next_retry_at ASC\\s+LIMIT \\$2").
			WithArgs(testTime, 50).
			WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(uuid.NewString())...))

		got, err := NewDeliveryRepository(mock).ListDueForRetry(context.Background(), testTime, 50)
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(testTime, 50).WillReturnError(errBoom)

		_, err := NewDeliveryRepository(mock).ListDueForRetry(context.Background(), testTime, 50)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "list due") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock := newMock(t)
		bad := deliveryRow(uuid.NewString())
		bad[9] = "not-a-time"
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(testTime, 50).WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(bad...))

		_, err := NewDeliveryRepository(mock).ListDueForRetry(context.Background(), testTime, 50)
		if err == nil || !strings.Contains(err.Error(), "delivery repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM webhook_deliveries").WithArgs(testTime, 50).
			WillReturnRows(pgxmock.NewRows(deliveryCols).AddRow(deliveryRow(uuid.NewString())...).RowError(0, errBoom))

		_, err := NewDeliveryRepository(mock).ListDueForRetry(context.Background(), testTime, 50)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
