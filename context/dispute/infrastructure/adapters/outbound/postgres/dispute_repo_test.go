package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var disputeCols = []string{
	"id", "tenant_id", "payment_id", "psp_reference", "amount", "currency",
	"reason", "status", "response_note", "resolved_note",
	"deadline", "opened_at", "responded_at", "resolved_at", "version",
}

var evidenceCols = []string{"dispute_id", "id", "evidence_type", "reference", "description", "submitted_at"}

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

var (
	testTime     = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	testDeadline = testTime.Add(7 * 24 * time.Hour)
)

// disputeRow arma la fila principal de una disputa (sin responded/resolved).
func disputeRow(id, tenantID string) []any {
	return []any{
		id, tenantID, "pay-1", "psp-1", int64(5000), "ARS",
		"fraudulent", "open", "", "",
		testDeadline, testTime, (*time.Time)(nil), (*time.Time)(nil), int64(1),
	}
}

// capturedRef arma el PaymentRef que OpenDisputeUseCase lee vía PaymentReader
// antes de construir la disputa.
func capturedRef(id string) domain.PaymentRef {
	return domain.PaymentRef{ID: id, TenantID: uuid.NewString(), Status: "captured", Amount: 5000, Currency: "ARS"}
}

func TestDisputeRepository_Save(t *testing.T) {
	d, err := domain.NewDispute(domain.NewDisputeID(),
		capturedRef("pay-1"), "psp-1", int64(5000), "ARS", domain.ReasonFraudulent, testDeadline, testTime)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}

	t.Run("inserts dispute and its evidence", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)

		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(15)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(ctx, d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("also inserts evidence rows", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		withEv, err := domain.NewDispute(domain.NewDisputeID(),
			capturedRef("pay-2"), "psp-2", int64(5000), "ARS", domain.ReasonFraudulent, testDeadline, testTime)
		if err != nil {
			t.Fatalf("new dispute: %v", err)
		}
		ev, err := domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d", testTime)
		if err != nil {
			t.Fatalf("new evidence: %v", err)
		}
		if err := withEv.Contest([]domain.Evidence{ev}, "", testTime); err != nil {
			t.Fatalf("contest: %v", err)
		}

		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(15)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO dispute_evidence").WithArgs(anyArgs(7)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(ctx, withEv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("insert error propagated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("INSERT INTO disputes").WillReturnError(errors.New("boom"))
		if err := repo.Save(ctx, d); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_Update(t *testing.T) {
	d, err := domain.NewDispute(domain.NewDisputeID(),
		capturedRef("pay-1"), "psp-1", int64(5000), "ARS", domain.ReasonFraudulent, testDeadline, testTime)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}

	t.Run("updates dispute", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("UPDATE disputes SET.*version=\\$7\\s+WHERE id=\\$1 AND version=\\$8").
			WithArgs(d.ID().String(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
				d.Version(), d.Version()-1).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(ctx, d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	// Otra transición ganó la carrera: el UPDATE no encuentra la versión leída.
	t.Run("stale version returns ErrOptimisticLock", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectExec("UPDATE disputes").WithArgs(anyArgs(8)...).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		if err := NewDisputeRepository(mock).Update(ctx, d); !errors.Is(err, domain.ErrOptimisticLock) {
			t.Fatalf("expected ErrOptimisticLock, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("evidence must not be written: %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("UPDATE disputes").WillReturnError(errors.New("boom"))
		if err := repo.Update(ctx, d); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_FindByID(t *testing.T) {
	id := domain.NewDisputeID()
	tid := uuid.NewString()

	t.Run("returns dispute with evidence", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)

		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid)...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows(evidenceCols).
				AddRow(id.String(), uuid.NewString(), "receipt", "ref", "desc", testTime))

		d, err := repo.FindByID(ctx, id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ID() != id || d.PaymentID() != "pay-1" || d.Status() != domain.StatusOpen {
			t.Errorf("unexpected dispute: %+v", d)
		}
		if len(d.Evidence()) != 1 || d.Evidence()[0].EvidenceType() != "receipt" {
			t.Errorf("evidence not loaded: %+v", d.Evidence())
		}
	})

	t.Run("no rows maps to ErrDisputeNotFound", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols))

		_, err := repo.FindByID(ctx, id)
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("evidence query error propagated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid)...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnError(errors.New("boom"))

		if _, err := repo.FindByID(ctx, id); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_FindByPaymentID(t *testing.T) {
	t.Run("not found returns ErrDisputeNotFound", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE payment_id=\\$1").WithArgs("pay-x").
			WillReturnRows(pgxmock.NewRows(disputeCols))

		_, err := repo.FindByPaymentID(ctx, "pay-x")
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("found returns dispute", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("FROM disputes WHERE payment_id=\\$1").WithArgs("pay-1").
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		d, err := repo.FindByPaymentID(ctx, "pay-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.PaymentID() != "pay-1" {
			t.Errorf("payment id = %q", d.PaymentID())
		}
	})
}

func TestDisputeRepository_ListByTenant(t *testing.T) {
	tid := domain.TenantID(uuid.NewString())

	t.Run("without status filter", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY opened_at DESC LIMIT \\$2").
			WithArgs(tid.String(), 50).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListByTenant(ctx, tid, "", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("with status filter", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE tenant_id=\\$1 AND status=\\$2 ORDER BY opened_at DESC LIMIT \\$3").
			WithArgs(tid.String(), "open", 50).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListByTenant(ctx, tid, "open", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("WHERE tenant_id=\\$1").WillReturnError(errors.New("boom"))
		if _, err := repo.ListByTenant(ctx, tid, "", 50); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_ListOverdue(t *testing.T) {
	t.Run("returns overdue disputes", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE status='open' AND deadline < \\$1").
			WithArgs(testTime, 100).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListOverdue(ctx, testTime, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("WHERE status='open'").WillReturnError(errors.New("boom"))
		if _, err := repo.ListOverdue(ctx, testTime, 100); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_Save_evidenceError(t *testing.T) {
	mock, ctx := newTxMock(t)
	repo := NewDisputeRepository(mock)
	d, err := domain.NewDispute(domain.NewDisputeID(),
		capturedRef("pay-1"), "psp-1", int64(5000), "ARS", domain.ReasonFraudulent, testDeadline, testTime)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}
	ev, err := domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d", testTime)
	if err != nil {
		t.Fatalf("new evidence: %v", err)
	}
	if err := d.Contest([]domain.Evidence{ev}, "", testTime); err != nil {
		t.Fatalf("contest: %v", err)
	}

	mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(15)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO dispute_evidence").WillReturnError(errors.New("boom"))

	if err := repo.Save(ctx, d); err == nil {
		t.Fatal("expected evidence insert error")
	}
}

func TestDisputeRepository_scanErrors(t *testing.T) {
	id := domain.NewDisputeID()

	t.Run("main scan error (bad type) surfaces", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		// amount como string no asignable a int64 → error de scan (no NoRows).
		bad := disputeRow(id.String(), uuid.NewString())
		bad[4] = "not-an-int"
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(bad...))

		_, err := repo.FindByID(ctx, id)
		if err == nil || errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected generic scan error, got %v", err)
		}
	})

	t.Run("evidence scan error surfaces", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		// Fila de evidencia con menos columnas → error de scan.
		mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
			WillReturnRows(pgxmock.NewRows([]string{"id", "evidence_type"}).AddRow("e1", "receipt"))

		if _, err := repo.FindByID(ctx, id); err == nil {
			t.Fatal("expected evidence scan error")
		}
	})
}

func TestDisputeRepository_ListByTenant_scanError(t *testing.T) {
	mock, ctx := newTxMock(t)
	repo := NewDisputeRepository(mock)
	tid := domain.TenantID(uuid.NewString())
	id := domain.NewDisputeID()
	mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY").WithArgs(tid.String(), 50).
		WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
	// La carga de evidencia falla dentro del loop → scanWithEvidence retorna error.
	mock.ExpectQuery("FROM dispute_evidence").WithArgs([]string{id.String()}).
		WillReturnError(errors.New("boom"))

	if _, err := repo.ListByTenant(ctx, tid, "", 50); err == nil {
		t.Fatal("expected error from scanWithEvidence in loop")
	}
}

func TestNullStr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("non-empty mapped wrong: %v", got)
	}
}

// Un pago inexistente se informaba como ErrDisputeNotFound ("dispute not found").
func TestPaymentReader_FindPayment(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectQuery("FROM payments").WithArgs("pay-x").WillReturnError(pgx.ErrNoRows)
		_, err := NewPaymentReader(mock).FindPayment(ctx, "pay-x")
		if !errors.Is(err, domain.ErrPaymentNotFound) || errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrPaymentNotFound, got %v", err)
		}
	})
	t.Run("found", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectQuery("FROM payments").WithArgs("pay-1").WillReturnRows(
			pgxmock.NewRows([]string{"id", "tenant_id", "status", "amount", "currency"}).
				AddRow("pay-1", "11111111-1111-1111-1111-111111111111", "captured", int64(5000), "ARS"))
		ref, err := NewPaymentReader(mock).FindPayment(ctx, "pay-1")
		if err != nil || ref.Status != "captured" || ref.Amount != 5000 || ref.Currency != "ARS" {
			t.Fatalf("ref = %+v, %v", ref, err)
		}
	})
}

// Fuera de una tx, cada método abre una sesión efímera con la variable RLS
// que corresponde: el tenant de la disputa o, para el poller, el operador.
func TestRLSSession(t *testing.T) {
	t.Run("save sets the tenant of the dispute", func(t *testing.T) {
		mock := newMock(t)
		d, err := domain.NewDispute(domain.NewDisputeID(), capturedRef("pay-1"), "psp", 5000, "ARS", domain.ReasonFraudulent, testDeadline, testTime)
		if err != nil {
			t.Fatalf("new dispute: %v", err)
		}
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(rlsTenantKey, d.TenantID().String()).WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(15)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		if err := NewDisputeRepository(mock).Save(context.Background(), d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("list overdue runs as platform operator", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(pkgpostgres.PlatformOperatorKey, "on").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("FROM disputes").WithArgs(testTime, 100).WillReturnRows(pgxmock.NewRows(disputeCols))
		mock.ExpectCommit()
		if _, err := NewDisputeRepository(mock).ListOverdue(context.Background(), testTime, 100); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})
}

// Si la carrera la gana la constraint UNIQUE(payment_id), el repo devuelve el
// centinela que el caso de uso traduce a 409 (antes, error genérico → 500).
func TestDisputeRepository_Save_DuplicatePayment(t *testing.T) {
	d, err := domain.NewDispute(domain.NewDisputeID(), capturedRef("pay-1"), "psp", 5000, "ARS", domain.ReasonFraudulent, testDeadline, testTime)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}
	for name, tc := range map[string]struct {
		pgErr *pgconn.PgError
		dup   bool
	}{
		"payment unique":   {&pgconn.PgError{Code: "23505", ConstraintName: "disputes_payment_id_key"}, true},
		"other unique":     {&pgconn.PgError{Code: "23505", ConstraintName: "disputes_pkey"}, false},
		"other error code": {&pgconn.PgError{Code: "23503"}, false},
	} {
		mock, ctx := newTxMock(t)
		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(15)...).WillReturnError(tc.pgErr)
		err := NewDisputeRepository(mock).Save(ctx, d)
		if errors.Is(err, application.ErrDuplicatePaymentKey) != tc.dup || err == nil {
			t.Errorf("%s: err = %v, duplicate = %v", name, err, tc.dup)
		}
	}
}

// La evidencia de un listado se carga en una sola query (antes, una por
// disputa) y se reparte por disputa.
func TestDisputeRepository_ListLoadsEvidenceInOneQuery(t *testing.T) {
	mock, ctx := newTxMock(t)
	tid := uuid.NewString()
	a, b := uuid.NewString(), uuid.NewString()
	mock.ExpectQuery("FROM disputes").WithArgs(tid, 50).
		WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(a, tid)...).AddRow(disputeRow(b, tid)...))
	mock.ExpectQuery("FROM dispute_evidence WHERE dispute_id = ANY").WithArgs([]string{a, b}).
		WillReturnRows(pgxmock.NewRows(evidenceCols).
			AddRow(b, uuid.NewString(), "receipt", "r1", "", testTime).
			AddRow(b, uuid.NewString(), "tracking", "r2", "", testTime))

	out, err := NewDisputeRepository(mock).ListByTenant(ctx, domain.TenantID(tid), "", 50)
	if err != nil || len(out) != 2 {
		t.Fatalf("list = %d, %v", len(out), err)
	}
	if len(out[0].Evidence()) != 0 || len(out[1].Evidence()) != 2 {
		t.Errorf("evidence per dispute = %d/%d, want 0/2", len(out[0].Evidence()), len(out[1].Evidence()))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
