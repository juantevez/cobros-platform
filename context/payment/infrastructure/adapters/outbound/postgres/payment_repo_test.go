package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payment/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var errBoom = errors.New("boom")

var paymentCols = []string{
	"id", "tenant_id", "idempotency_key", "checkout_id",
	"amount", "currency", "platform_fee", "psp_fee",
	"payer_name", "payer_email", "payer_doc_type", "payer_doc_num",
	"payment_method", "psp_name", "psp_reference",
	"status", "failure_reason", "metadata",
	"authorized_at", "captured_at", "failed_at",
	"created_at", "updated_at",
	"refund_psp_reference", "refunded_at", "psp_failure_detail",
}

var testTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// newTxMock devuelve el mock y un contexto con una tx abierta sobre él, como
// el de TxManager.RunInTx: RunWithAppTenant delega en esa tx (las variables
// RLS ya las fijó el TxManager) y las queries llegan al mock igual que sin
// sesión. El seteo de la sesión se prueba en TestRLSSession y la aislación
// real contra Postgres en rls_test.go.
func newTxMock(t *testing.T) (pgxmock.PgxPoolIface, context.Context) {
	t.Helper()
	mock := newMock(t)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return mock, pkgpostgres.WithTx(context.Background(), tx)
}

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

// newPayment crea un pago initiated con pagador parcial (sin documento).
func newPayment(t *testing.T, meta map[string]string) *domain.Payment {
	t.Helper()
	amount, _ := domain.NewMoney(10_000, "ARS")
	p, err := domain.NewPayment(domain.NewPaymentID(), domain.TenantID(uuid.NewString()), "idem-1", amount,
		domain.PayerInfo{Name: "Ana", Email: "ana@example.com"}, domain.MethodCard, meta)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	return p
}

// capturedRow arma una fila de un pago capturado con nullables en NULL.
func capturedRow(id, tenantID string) []any {
	captured := testTime.Add(time.Minute)
	return []any{
		id, tenantID, "idem-1", (*string)(nil),
		int64(10_000), "ARS", int64(300), int64(150),
		strPtr("Ana"), strPtr("ana@example.com"), (*string)(nil), (*string)(nil),
		"card", strPtr("mock"), strPtr("PSP-1"),
		"captured", (*string)(nil), []byte(`{"order":"1"}`),
		(*time.Time)(nil), &captured, (*time.Time)(nil),
		testTime, captured,
		(*string)(nil), (*time.Time)(nil), (*string)(nil),
	}
}

func TestPaymentRepository_Save(t *testing.T) {
	t.Run("inserts all columns with NULL for empty strings and JSON metadata", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		p := newPayment(t, map[string]string{"order": "1"})

		mock.ExpectExec("INSERT INTO payments").WithArgs(
			p.ID().String(), p.TenantID().String(), "idem-1", (*string)(nil),
			int64(10_000), "ARS", int64(0), int64(0),
			strPtr("Ana"), strPtr("ana@example.com"), (*string)(nil), (*string)(nil),
			"card", (*string)(nil), (*string)(nil),
			"initiated", (*string)(nil), []byte(`{"order":"1"}`),
			(*time.Time)(nil), (*time.Time)(nil), (*time.Time)(nil),
			p.CreatedAt(), p.UpdatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(ctx, p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("nil metadata is stored as JSON null", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		args := anyArgs(23)
		args[17] = []byte("null")

		mock.ExpectExec("INSERT INTO payments").WithArgs(args...).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(ctx, newPayment(t, nil)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectExec("INSERT INTO payments").WithArgs(anyArgs(23)...).WillReturnError(errBoom)

		err := repo.Save(ctx, newPayment(t, nil))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payment repo: save") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestPaymentRepository_Update(t *testing.T) {
	t.Run("updates mutable columns", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		p := newPayment(t, nil)
		_ = p.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))
		_ = p.Capture("mock", "PSP-1", domain.ReconstituteMoney(300, "ARS"), domain.ReconstituteMoney(150, "ARS"))

		mock.ExpectExec("UPDATE payments SET").WithArgs(
			p.ID().String(),
			"captured", strPtr("mock"), strPtr("PSP-1"),
			int64(300), int64(150),
			(*string)(nil),
			(*time.Time)(nil), p.CapturedAt(), (*time.Time)(nil),
			p.UpdatedAt(),
			"processing",
			(*string)(nil), (*time.Time)(nil),
			(*string)(nil),
		).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(ctx, p, domain.StatusProcessing); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectExec("UPDATE payments SET").WithArgs(anyArgs(15)...).WillReturnError(errBoom)

		err := repo.Update(ctx, newPayment(t, nil), domain.StatusInitiated)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payment repo: update") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	// El request original y el recuperador pueden resolver el mismo pago:
	// el UPDATE exige el estado de partida y el que llega tarde no escribe.
	t.Run("payment no longer in the from status returns ErrPaymentChanged", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectExec(`UPDATE payments SET[\s\S]*WHERE id=\$1 AND status=\$12`).WithArgs(anyArgs(15)...).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		err := NewPaymentRepository(mock).Update(ctx, newPayment(t, nil), domain.StatusProcessing)
		if !errors.Is(err, domain.ErrPaymentChanged) {
			t.Fatalf("expected ErrPaymentChanged, got %v", err)
		}
	})
}

func TestPaymentRepository_FindByID(t *testing.T) {
	id, tenantID := uuid.NewString(), uuid.NewString()

	t.Run("reconstitutes payment from row", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectQuery("FROM payments WHERE id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(paymentCols).AddRow(capturedRow(id, tenantID)...))

		p, err := repo.FindByID(ctx, domain.PaymentID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID().String() != id || p.TenantID().String() != tenantID || p.IdempotencyKey() != "idem-1" || p.CheckoutID() != "" {
			t.Errorf("identity not mapped")
		}
		if p.Amount().Amount() != 10_000 || p.PlatformFee().Amount() != 300 || p.PSPFee().Amount() != 150 ||
			p.PlatformFee().Currency() != "ARS" || p.PSPFee().Currency() != "ARS" {
			t.Errorf("amounts = %v %v %v", p.Amount(), p.PlatformFee(), p.PSPFee())
		}
		if want := (domain.PayerInfo{Name: "Ana", Email: "ana@example.com"}); p.PayerInfo() != want {
			t.Errorf("payer = %+v, want %+v", p.PayerInfo(), want)
		}
		if p.Method() != domain.MethodCard || p.PSPName() != "mock" || p.PSPReference() != "PSP-1" {
			t.Errorf("method/psp = %q %q %q", p.Method(), p.PSPName(), p.PSPReference())
		}
		if p.Status() != domain.StatusCaptured || p.FailureReason() != "" {
			t.Errorf("status=%q reason=%q", p.Status(), p.FailureReason())
		}
		if !reflect.DeepEqual(p.Metadata(), map[string]string{"order": "1"}) {
			t.Errorf("metadata = %v", p.Metadata())
		}
		if p.AuthorizedAt() != nil || p.CapturedAt() == nil || p.FailedAt() != nil {
			t.Error("nullable timestamps not mapped")
		}
		if !p.CreatedAt().Equal(testTime) || p.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", p.CreatedAt())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("empty or invalid metadata yields nil map", func(t *testing.T) {
		for _, meta := range [][]byte{nil, []byte("not-json")} {
			mock, ctx := newTxMock(t)
			repo := NewPaymentRepository(mock)
			row := capturedRow(id, tenantID)
			row[17] = meta

			mock.ExpectQuery("FROM payments WHERE id=").WithArgs(id).
				WillReturnRows(pgxmock.NewRows(paymentCols).AddRow(row...))

			p, err := repo.FindByID(ctx, domain.PaymentID(id))
			if err != nil {
				t.Fatalf("metadata %q: unexpected error: %v", meta, err)
			}
			if p.Metadata() != nil {
				t.Errorf("metadata %q: got %v, want nil", meta, p.Metadata())
			}
		}
	})

	t.Run("no rows maps to ErrPaymentNotFound", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectQuery("FROM payments WHERE id=").WithArgs(id).WillReturnRows(pgxmock.NewRows(paymentCols))

		_, err := repo.FindByID(ctx, domain.PaymentID(id))
		if !errors.Is(err, domain.ErrPaymentNotFound) {
			t.Fatalf("expected ErrPaymentNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectQuery("FROM payments WHERE id=").WithArgs(id).WillReturnError(errBoom)

		_, err := repo.FindByID(ctx, domain.PaymentID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payment repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestPaymentRepository_FindByIdempotencyKey(t *testing.T) {
	id, tenantID := uuid.NewString(), uuid.NewString()

	t.Run("filters by tenant and key", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectQuery("WHERE tenant_id=\\$1 AND idempotency_key=\\$2").WithArgs(tenantID, "idem-1").
			WillReturnRows(pgxmock.NewRows(paymentCols).AddRow(capturedRow(id, tenantID)...))

		p, err := repo.FindByIdempotencyKey(ctx, domain.TenantID(tenantID), "idem-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID().String() != id {
			t.Errorf("id = %s, want %s", p.ID(), id)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no rows maps to ErrPaymentNotFound", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)

		mock.ExpectQuery("idempotency_key").WithArgs(tenantID, "missing").WillReturnRows(pgxmock.NewRows(paymentCols))

		_, err := repo.FindByIdempotencyKey(ctx, domain.TenantID(tenantID), "missing")
		if !errors.Is(err, domain.ErrPaymentNotFound) {
			t.Fatalf("expected ErrPaymentNotFound, got %v", err)
		}
	})
}

func TestNullStrDeref(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty string should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("nullStr(\"x\") = %v", got)
	}
	if deref(nil) != "" || deref(strPtr("y")) != "y" {
		t.Error("deref mismatch")
	}
}

func TestPaymentRepository_ClaimProcessing(t *testing.T) {
	// El WHERE condicional (status=$4 = initiated) es lo que hace atómico el claim.
	claimSQL := `UPDATE payments SET status=\$2, updated_at=\$3, platform_fee=\$5\s+WHERE id=\$1 AND status=\$4`

	t.Run("claims when the payment is still initiated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		p := newPayment(t, nil)
		_ = p.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))

		mock.ExpectExec(claimSQL).
			WithArgs(p.ID().String(), "processing", p.UpdatedAt(), "initiated", int64(300)).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		claimed, err := repo.ClaimProcessing(ctx, p)
		if err != nil || !claimed {
			t.Fatalf("claimed=%v err=%v, want true nil", claimed, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	t.Run("not claimed when another execution already moved it", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		p := newPayment(t, nil)
		_ = p.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))

		mock.ExpectExec(claimSQL).
			WithArgs(anyArgs(5)...).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		claimed, err := repo.ClaimProcessing(ctx, p)
		if err != nil || claimed {
			t.Fatalf("claimed=%v err=%v, want false nil", claimed, err)
		}
	})

	t.Run("db error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPaymentRepository(mock)
		p := newPayment(t, nil)

		mock.ExpectExec(claimSQL).WithArgs(anyArgs(5)...).WillReturnError(errBoom)

		_, err := repo.ClaimProcessing(ctx, p)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "claim processing") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestPaymentRepository_ListStuckProcessing(t *testing.T) {
	mock, ctx := newTxMock(t)
	cut := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	id, tenantID := uuid.NewString(), uuid.NewString()
	mock.ExpectQuery(`FROM payments WHERE status=\$1 AND updated_at < \$2 ORDER BY updated_at LIMIT \$3`).
		WithArgs("processing", cut, 50).
		WillReturnRows(pgxmock.NewRows(paymentCols).AddRow(capturedRow(id, tenantID)...))
	got, err := NewPaymentRepository(mock).ListStuckProcessing(ctx, cut, 50)
	if err != nil || len(got) != 1 || got[0].ID().String() != id {
		t.Fatalf("got %d, %v", len(got), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// Dos requests con la misma clave: el segundo INSERT choca con la constraint
// y el repo lo informa como ErrPaymentAlreadyExists (antes, error genérico → 500).
func TestPaymentRepository_Save_DuplicateKey(t *testing.T) {
	for name, tc := range map[string]struct {
		pgErr *pgconn.PgError
		dup   bool
	}{
		"idempotency unique": {&pgconn.PgError{Code: "23505", ConstraintName: "payments_tenant_id_idempotency_key_key"}, true},
		"other unique":       {&pgconn.PgError{Code: "23505", ConstraintName: "payments_pkey"}, false},
	} {
		mock, ctx := newTxMock(t)
		mock.ExpectExec("INSERT INTO payments").WithArgs(anyArgs(23)...).WillReturnError(tc.pgErr)
		err := NewPaymentRepository(mock).Save(ctx, newPayment(t, nil))
		if errors.Is(err, domain.ErrPaymentAlreadyExists) != tc.dup || err == nil {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Fuera de una tx, cada método abre una sesión efímera con la variable RLS
// que corresponde: el tenant del pago o, para el recuperador, el operador.
func TestRLSSession(t *testing.T) {
	t.Run("save sets the tenant of the payment", func(t *testing.T) {
		mock := newMock(t)
		p := newPayment(t, nil)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(rlsTenantKey, p.TenantID().String()).WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec("INSERT INTO payments").WithArgs(anyArgs(23)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		if err := NewPaymentRepository(mock).Save(context.Background(), p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("list stuck runs as platform operator", func(t *testing.T) {
		mock := newMock(t)
		cut := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(pkgpostgres.PlatformOperatorKey, "on").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("FROM payments").WithArgs("processing", cut, 50).WillReturnRows(pgxmock.NewRows(paymentCols))
		mock.ExpectCommit()
		if _, err := NewPaymentRepository(mock).ListStuckProcessing(context.Background(), cut, 50); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})
}
