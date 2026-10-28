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

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var alertCols = []string{
	"id", "tenant_id", "alert_type", "risk_level", "status", "subject", "score",
	"details", "note", "resolved_by", "created_at", "resolved_at",
	"proposed_status", "proposed_by", "proposed_at", "approval_note",
}

// noProposal son las 4 columnas four-eyes vacías de una alerta sin propuesta.
var noProposal = []any{"", "", (*time.Time)(nil), ""}

func alertRow(vals ...any) []any { return append(vals, noProposal...) }

// expectRLSTx espera la tx efímera de RunWithAppTenant que fija app.current_tenant.
func expectRLSTx(mock pgxmock.PgxPoolIface, tenant string) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs(rlsTenantKey, tenant).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
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

	snapshot := func(t *testing.T, status domain.AlertStatus) domain.AlertSnapshot {
		return domain.AlertSnapshot{
			ID: domain.NewAlertID(), TenantID: tenantID(t), Type: domain.AlertSanctionsMatch,
			RiskLevel: domain.RiskHigh, Status: status, Subject: "subj", Score: 95, CreatedAt: now,
		}
	}

	t.Run("final disposition persisted conditionally on previous status", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		snap := snapshot(t, domain.StatusConfirmed)
		snap.Note, snap.ResolvedBy, snap.ResolvedAt = "confirmado", "officer-2", &resolved
		snap.ProposedStatus, snap.ProposedBy, snap.ProposedAt, snap.ApprovalNote = domain.StatusConfirmed, "officer-1", &now, "ok"
		a := domain.ReconstituteAlert(snap)

		mock.ExpectExec(`UPDATE aml_alerts\s+SET status=\$3, .*approval_note=\$10\s+WHERE id=\$1 AND status=\$2`).
			WithArgs(a.ID().String(), "pending_second_approval", "confirmed", pgxmock.AnyArg(),
				&resolved, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), &now, pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(context.Background(), a, domain.StatusPendingSecondApproval); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.ReconstituteAlert(snapshot(t, domain.StatusCleared))
		mock.ExpectExec("UPDATE aml_alerts").WillReturnError(errors.New("boom"))
		if err := repo.Update(context.Background(), a, domain.StatusOpen); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("concurrent review yields ErrAlertNotOpen", func(t *testing.T) {
		// Concurrencia optimista: si otra transacción ya movió la alerta desde
		// `from`, el UPDATE no toca filas y el caso de uso aborta sin publicar.
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		a := domain.ReconstituteAlert(snapshot(t, domain.StatusCleared))
		mock.ExpectExec(`UPDATE aml_alerts .* AND status=\$2`).
			WithArgs(append([]any{a.ID().String(), "open"}, anyArgs(8)...)...).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		if err := repo.Update(context.Background(), a, domain.StatusOpen); !errors.Is(err, domain.ErrAlertNotOpen) {
			t.Fatalf("err = %v, want ErrAlertNotOpen", err)
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
		rows := pgxmock.NewRows(alertCols).AddRow(alertRow(
			id.String(), tid.String(), "sanctions_match", "high", "open", "subj", 95,
			[]byte(`{"list":"OFAC"}`), "nota", "", now, (*time.Time)(nil),
		)...)
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

	t.Run("pending proposal is restored under RLS session", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		id := domain.NewAlertID()
		tid := tenantID(t)
		expectRLSTx(mock, tid.String())
		rows := pgxmock.NewRows(alertCols).AddRow(
			id.String(), tid.String(), "sanctions_match", "high", "pending_second_approval", "subj", 95,
			[]byte(`{}`), "coincide", "", now, (*time.Time)(nil),
			"confirmed", "officer-1", &now, "",
		)
		mock.ExpectQuery("FROM aml_alerts WHERE id=\\$1").WithArgs(id.String()).WillReturnRows(rows)
		mock.ExpectCommit()

		ctx := pkgpostgres.WithTenantID(context.Background(), tid.String())
		a, err := repo.FindByID(ctx, id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != domain.StatusPendingSecondApproval || a.ProposedStatus() != domain.StatusConfirmed ||
			a.ProposedBy() != "officer-1" || a.ProposedAt() == nil {
			t.Errorf("proposal not restored: %+v", a)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
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

func TestAlertRepository_List(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	makeRows := func(tid domain.TenantID) *pgxmock.Rows {
		return pgxmock.NewRows(alertCols).AddRow(alertRow(
			domain.NewAlertID().String(), tid.String(), "sanctions_match", "high", "open",
			"subj", 95, []byte(`{}`), "", "", now, (*time.Time)(nil),
		)...)
	}
	operatorCtx := func() context.Context {
		return pkgpostgres.WithPlatformOperator(context.Background())
	}
	expectOperatorTx := func(mock pgxmock.PgxPoolIface) {
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(pkgpostgres.PlatformOperatorKey, "on").
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
	}

	t.Run("cross-tenant without filters", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		expectOperatorTx(mock)
		mock.ExpectQuery("FROM aml_alerts ORDER BY created_at DESC LIMIT \\$1").
			WithArgs(50).WillReturnRows(makeRows(tenantID(t)))
		mock.ExpectCommit()

		out, err := repo.List(operatorCtx(), application.AlertFilter{}, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(out))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("tenant and status filters add predicates", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		tid := tenantID(t)
		expectRLSTx(mock, tid.String())
		mock.ExpectQuery("WHERE tenant_id=\\$1 AND status=\\$2 ORDER BY created_at DESC LIMIT \\$3").
			WithArgs(tid.String(), "pending_second_approval", 50).WillReturnRows(makeRows(tid))
		mock.ExpectCommit()

		filter := application.AlertFilter{TenantID: tid, Status: domain.StatusPendingSecondApproval}
		if _, err := repo.List(context.Background(), filter, 50); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("status filter only", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		expectOperatorTx(mock)
		mock.ExpectQuery("FROM aml_alerts WHERE status=\\$1 ORDER BY created_at DESC LIMIT \\$2").
			WithArgs("open", 50).WillReturnRows(makeRows(tenantID(t)))
		mock.ExpectCommit()
		if _, err := repo.List(operatorCtx(), application.AlertFilter{Status: domain.StatusOpen}, 50); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		expectOperatorTx(mock)
		mock.ExpectQuery("FROM aml_alerts").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := repo.List(operatorCtx(), application.AlertFilter{}, 50); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAlertRepository(mock)
		expectOperatorTx(mock)
		// score como string no asignable a int → error de scan.
		badRows := pgxmock.NewRows(alertCols).AddRow(alertRow(
			domain.NewAlertID().String(), tenantID(t).String(), "sanctions_match", "high", "open",
			"subj", "not-an-int", []byte(`{}`), "", "", now, (*time.Time)(nil),
		)...)
		mock.ExpectQuery("FROM aml_alerts").WithArgs(50).WillReturnRows(badRows)
		mock.ExpectRollback()
		if _, err := repo.List(operatorCtx(), application.AlertFilter{}, 50); err == nil {
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
