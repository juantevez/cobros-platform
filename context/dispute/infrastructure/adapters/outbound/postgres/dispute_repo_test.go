package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

var disputeCols = []string{
	"id", "tenant_id", "payment_id", "psp_reference", "amount", "currency",
	"reason", "status", "response_note", "resolved_note",
	"deadline", "opened_at", "responded_at", "resolved_at",
}

var evidenceCols = []string{"id", "evidence_type", "reference", "description", "submitted_at"}

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
		testDeadline, testTime, (*time.Time)(nil), (*time.Time)(nil),
	}
}

func TestDisputeRepository_Save(t *testing.T) {
	d, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID(uuid.NewString()),
		"pay-1", "psp-1", 5000, "ARS", domain.ReasonFraudulent, testDeadline)

	t.Run("inserts dispute and its evidence", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)

		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(14)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(context.Background(), d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("also inserts evidence rows", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		withEv, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID(uuid.NewString()),
			"pay-2", "psp-2", 5000, "ARS", domain.ReasonFraudulent, testDeadline)
		_ = withEv.Contest([]domain.Evidence{domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d")}, "", testTime)

		mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(14)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO dispute_evidence").WithArgs(anyArgs(6)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(context.Background(), withEv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("insert error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("INSERT INTO disputes").WillReturnError(errors.New("boom"))
		if err := repo.Save(context.Background(), d); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_Update(t *testing.T) {
	d, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID(uuid.NewString()),
		"pay-1", "psp-1", 5000, "ARS", domain.ReasonFraudulent, testDeadline)

	t.Run("updates dispute", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("UPDATE disputes").WithArgs(anyArgs(6)...).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(context.Background(), d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectExec("UPDATE disputes").WillReturnError(errors.New("boom"))
		if err := repo.Update(context.Background(), d); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_FindByID(t *testing.T) {
	id := domain.NewDisputeID()
	tid := uuid.NewString()

	t.Run("returns dispute with evidence", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)

		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid)...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(evidenceCols).
				AddRow(uuid.NewString(), "receipt", "ref", "desc", testTime))

		d, err := repo.FindByID(context.Background(), id)
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
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols))

		_, err := repo.FindByID(context.Background(), id)
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("evidence query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid)...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnError(errors.New("boom"))

		if _, err := repo.FindByID(context.Background(), id); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_FindByPaymentID(t *testing.T) {
	t.Run("not found returns ErrDisputeNotFound", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE payment_id=\\$1").WithArgs("pay-x").
			WillReturnRows(pgxmock.NewRows(disputeCols))

		_, err := repo.FindByPaymentID(context.Background(), "pay-x")
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("found returns dispute", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("FROM disputes WHERE payment_id=\\$1").WithArgs("pay-1").
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		d, err := repo.FindByPaymentID(context.Background(), "pay-1")
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
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY opened_at DESC LIMIT \\$2").
			WithArgs(tid.String(), 50).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListByTenant(context.Background(), tid, "", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("with status filter", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE tenant_id=\\$1 AND status=\\$2 ORDER BY opened_at DESC LIMIT \\$3").
			WithArgs(tid.String(), "open", 50).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListByTenant(context.Background(), tid, "open", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("WHERE tenant_id=\\$1").WillReturnError(errors.New("boom"))
		if _, err := repo.ListByTenant(context.Background(), tid, "", 50); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_ListOverdue(t *testing.T) {
	t.Run("returns overdue disputes", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		id := domain.NewDisputeID()
		mock.ExpectQuery("WHERE status='open' AND deadline < \\$1").
			WithArgs(testTime, 100).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(evidenceCols))

		out, err := repo.ListOverdue(context.Background(), testTime, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("WHERE status='open'").WillReturnError(errors.New("boom"))
		if _, err := repo.ListOverdue(context.Background(), testTime, 100); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDisputeRepository_Save_evidenceError(t *testing.T) {
	mock := newMock(t)
	repo := NewDisputeRepository(mock)
	d, _ := domain.NewDispute(domain.NewDisputeID(), domain.TenantID(uuid.NewString()),
		"pay-1", "psp-1", 5000, "ARS", domain.ReasonFraudulent, testDeadline)
	_ = d.Contest([]domain.Evidence{domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d")}, "", testTime)

	mock.ExpectExec("INSERT INTO disputes").WithArgs(anyArgs(14)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO dispute_evidence").WillReturnError(errors.New("boom"))

	if err := repo.Save(context.Background(), d); err == nil {
		t.Fatal("expected evidence insert error")
	}
}

func TestDisputeRepository_scanErrors(t *testing.T) {
	id := domain.NewDisputeID()

	t.Run("main scan error (bad type) surfaces", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		// amount como string no asignable a int64 → error de scan (no NoRows).
		bad := disputeRow(id.String(), uuid.NewString())
		bad[4] = "not-an-int"
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(bad...))

		_, err := repo.FindByID(context.Background(), id)
		if err == nil || errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected generic scan error, got %v", err)
		}
	})

	t.Run("evidence scan error surfaces", func(t *testing.T) {
		mock := newMock(t)
		repo := NewDisputeRepository(mock)
		mock.ExpectQuery("FROM disputes WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), uuid.NewString())...))
		// Fila de evidencia con menos columnas → error de scan.
		mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "evidence_type"}).AddRow("e1", "receipt"))

		if _, err := repo.FindByID(context.Background(), id); err == nil {
			t.Fatal("expected evidence scan error")
		}
	})
}

func TestDisputeRepository_ListByTenant_scanError(t *testing.T) {
	mock := newMock(t)
	repo := NewDisputeRepository(mock)
	tid := domain.TenantID(uuid.NewString())
	id := domain.NewDisputeID()
	mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY").WithArgs(tid.String(), 50).
		WillReturnRows(pgxmock.NewRows(disputeCols).AddRow(disputeRow(id.String(), tid.String())...))
	// La carga de evidencia falla dentro del loop → scanWithEvidence retorna error.
	mock.ExpectQuery("FROM dispute_evidence").WithArgs(id.String()).
		WillReturnError(errors.New("boom"))

	if _, err := repo.ListByTenant(context.Background(), tid, "", 50); err == nil {
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
