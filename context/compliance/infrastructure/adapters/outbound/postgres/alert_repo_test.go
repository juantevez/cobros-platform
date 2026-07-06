package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

var alertCols = []string{
	"id", "tenant_id", "alert_type", "risk_level", "status", "subject", "score",
	"details", "note", "created_at", "resolved_at",
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

// anyArgs devuelve n matchers AnyArg, para expectativas donde no importa el
// valor exacto de cada argumento sino que el conteo coincida.
func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

func tenantID(t *testing.T) domain.TenantID {
	t.Helper()
	tid, err := domain.ParseTenantID(uuid.NewString())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return tid
}

func TestAlertRepository_Save(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("inserts open alert", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.NewAlert(domain.NewAlertID(), tenantID(t), domain.AlertSanctionsMatch,
			domain.RiskHigh, "subj", 95, map[string]string{"list": "OFAC"}, now)

		mock.ExpectExec("INSERT INTO aml_alerts").
			WithArgs(
				a.ID().String(), a.TenantID().String(),
				"sanctions_match", "high", "open", "subj", 95,
				pgxmock.AnyArg(), // details json
				pgxmock.AnyArg(), // note (nil)
				now,
				(*time.Time)(nil), // resolved_at
			).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(context.Background(), a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("unique violation maps to ErrDuplicateAlert", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.NewAlert(domain.NewAlertID(), tenantID(t), domain.AlertSanctionsMatch,
			domain.RiskHigh, "subj", 95, nil, now)

		// Con WithArgs matcheando los 11 args, el Exec devuelve nuestro PgError
		// (sin él, pgxmock erraría por conteo de args antes de llegar al mapeo).
		mock.ExpectExec("INSERT INTO aml_alerts").
			WithArgs(anyArgs(11)...).
			WillReturnError(&pgconn.PgError{Code: "23505"})

		err := repo.Save(context.Background(), a)
		if !errors.Is(err, domain.ErrDuplicateAlert) {
			t.Fatalf("expected ErrDuplicateAlert, got %v", err)
		}
	})

	t.Run("other db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.NewAlert(domain.NewAlertID(), tenantID(t), domain.AlertSanctionsMatch,
			domain.RiskHigh, "subj", 95, nil, now)

		mock.ExpectExec("INSERT INTO aml_alerts").WillReturnError(errors.New("boom"))
		if err := repo.Save(context.Background(), a); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestAlertRepository_Update(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	resolved := now.Add(time.Hour)

	t.Run("updates status and resolution", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.ReconstituteAlert(domain.NewAlertID(), tenantID(t), domain.AlertSanctionsMatch,
			domain.RiskHigh, domain.StatusConfirmed, "subj", 95, nil, "confirmado", now, &resolved)

		mock.ExpectExec("UPDATE aml_alerts").
			WithArgs(a.ID().String(), "confirmed", pgxmock.AnyArg(), &resolved).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(context.Background(), a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.ReconstituteAlert(domain.NewAlertID(), tenantID(t), domain.AlertSanctionsMatch,
			domain.RiskHigh, domain.StatusCleared, "s", 1, nil, "", now, nil)
		mock.ExpectExec("UPDATE aml_alerts").WillReturnError(errors.New("boom"))
		if err := repo.Update(context.Background(), a); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestAlertRepository_FindByID(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("returns alert", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		id := domain.NewAlertID()
		tid := tenantID(t)
		rows := pgxmock.NewRows(alertCols).AddRow(
			id.String(), tid.String(), "sanctions_match", "high", "open", "subj", 95,
			[]byte(`{"list":"OFAC"}`), "nota", now, (*time.Time)(nil),
		)
		mock.ExpectQuery("FROM aml_alerts WHERE id=\\$1").WithArgs(id.String()).WillReturnRows(rows)

		a, err := repo.FindByID(context.Background(), id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.ID() != id || a.Status() != domain.StatusOpen || a.Score() != 95 {
			t.Errorf("unexpected alert: %+v", a)
		}
		if a.Details()["list"] != "OFAC" || a.Note() != "nota" {
			t.Errorf("details/note mismatch: %+v", a)
		}
	})

	t.Run("no rows maps to ErrAlertNotFound", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		id := domain.NewAlertID()
		mock.ExpectQuery("FROM aml_alerts WHERE id=\\$1").WithArgs(id.String()).
			WillReturnRows(pgxmock.NewRows(alertCols))

		_, err := repo.FindByID(context.Background(), id)
		if !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})
}

func TestAlertRepository_ListByTenant(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	makeRows := func(tid domain.TenantID) *pgxmock.Rows {
		return pgxmock.NewRows(alertCols).AddRow(
			domain.NewAlertID().String(), tid.String(), "sanctions_match", "high", "open",
			"subj", 95, []byte(`{}`), "", now, (*time.Time)(nil),
		)
	}

	t.Run("without status filter", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		tid := tenantID(t)
		// Sin filtro: args = [tenant, limit], sin "AND status".
		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY created_at DESC LIMIT \\$2").
			WithArgs(tid.String(), 50).WillReturnRows(makeRows(tid))

		out, err := repo.ListByTenant(context.Background(), tid, "", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(out))
		}
	})

	t.Run("with status filter adds predicate", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		tid := tenantID(t)
		mock.ExpectQuery("WHERE tenant_id=\\$1 AND status=\\$2 ORDER BY created_at DESC LIMIT \\$3").
			WithArgs(tid.String(), "open", 50).WillReturnRows(makeRows(tid))

		out, err := repo.ListByTenant(context.Background(), tid, "open", 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		tid := tenantID(t)
		mock.ExpectQuery("WHERE tenant_id=\\$1").WillReturnError(errors.New("boom"))
		if _, err := repo.ListByTenant(context.Background(), tid, "", 50); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		tid := tenantID(t)
		// score como string no asignable a int → error de scan.
		badRows := pgxmock.NewRows(alertCols).AddRow(
			domain.NewAlertID().String(), tid.String(), "sanctions_match", "high", "open",
			"subj", "not-an-int", []byte(`{}`), "", now, (*time.Time)(nil),
		)
		mock.ExpectQuery("WHERE tenant_id=\\$1").WithArgs(tid.String(), 50).WillReturnRows(badRows)
		if _, err := repo.ListByTenant(context.Background(), tid, "", 50); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

func TestNullStr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("non-empty mapped wrong: %v", got)
	}
}
