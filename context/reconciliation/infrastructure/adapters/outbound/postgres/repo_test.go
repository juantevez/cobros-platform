package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var errBoom = errors.New("boom")

var (
	testFrom    = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	testTo      = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	testCreated = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
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

// expectSession espera la tx efímera que fija app.current_tenant (RLS).
func expectSession(mock pgxmock.PgxPoolIface, tenant string) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs(rlsTenantKey, tenant).WillReturnResult(pgxmock.NewResult("SELECT", 1))
}

// tenantCtx es el contexto de un request autenticado del tenant.
func tenantCtx(tenant string) context.Context {
	return pkgpostgres.WithTenantID(context.Background(), tenant)
}

func TestNullStrWrapErr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty string should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("nullStr(\"x\") = %v", got)
	}
	if wrapErr("op", nil) != nil {
		t.Error("wrapErr(nil) should be nil")
	}
	if err := wrapErr("op", errBoom); !errors.Is(err, errBoom) || err.Error() != "reconciliation op: boom" {
		t.Errorf("wrapErr = %v", err)
	}
}

// ── RunRepository ─────────────────────────────────────────────────────────────

var runCols = []string{
	"id", "tenant_id", "type", "status", "period_from", "period_to",
	"total_records", "matched_count", "discrepancy_count",
	"error_msg", "created_at", "completed_at",
}

func runRow(id, tenantID string) []any {
	completed := testCreated.Add(time.Hour)
	return []any{
		id, tenantID, "payment", "completed", testFrom, testTo,
		10, 8, 2, "", testCreated, &completed,
	}
}

func TestRunRepository_Save(t *testing.T) {
	t.Run("inserts run with NULL tenant and error for global pending run", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), "", domain.TypeInternal, testFrom, testTo, testCreated)

		mock.ExpectExec("INSERT INTO reconciliation_runs").WithArgs(
			run.ID().String(), (*string)(nil),
			"internal_ledger", "pending",
			testFrom, testTo,
			0, 0, 0,
			(*string)(nil), run.CreatedAt(), (*time.Time)(nil),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := NewRunRepository(mock).Save(context.Background(), run); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("tenant run stores tenant id", func(t *testing.T) {
		mock := newMock(t)
		tenant := uuid.NewString()
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), domain.TenantID(tenant), domain.TypePayment, testFrom, testTo, testCreated)
		args := anyArgs(12)
		args[1] = strPtr(tenant)

		expectSession(mock, tenant)
		mock.ExpectExec("INSERT INTO reconciliation_runs").WithArgs(args...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()

		if err := NewRunRepository(mock).Save(context.Background(), run); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), "", domain.TypeInternal, testFrom, testTo, testCreated)
		mock.ExpectExec("INSERT INTO reconciliation_runs").WithArgs(anyArgs(12)...).WillReturnError(errBoom)

		err := NewRunRepository(mock).Save(context.Background(), run)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reconciliation save run") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestRunRepository_Update(t *testing.T) {
	t.Run("updates results of failed run conditioned on previous status", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), "", domain.TypePayment, testFrom, testTo, testCreated)
		_ = run.Start()
		_ = run.Fail("parse error", testCreated)

		mock.ExpectExec("UPDATE reconciliation_runs SET .* WHERE id=\\$1 AND status=\\$8").WithArgs(
			run.ID().String(), "failed",
			0, 0, 0,
			strPtr("parse error"), run.CompletedAt(), "running",
		).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := NewRunRepository(mock).Update(context.Background(), run, domain.RunStatusRunning); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no rows affected means another process changed the run", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), "", domain.TypePayment, testFrom, testTo, testCreated)
		_ = run.Start()
		mock.ExpectExec("UPDATE reconciliation_runs").WithArgs(anyArgs(8)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		err := NewRunRepository(mock).Update(context.Background(), run, domain.RunStatusPending)
		if !errors.Is(err, domain.ErrRunAlreadyRunning) || !strings.Contains(err.Error(), `no longer "pending"`) {
			t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), "", domain.TypePayment, testFrom, testTo, testCreated)
		mock.ExpectExec("UPDATE reconciliation_runs").WithArgs(anyArgs(8)...).WillReturnError(errBoom)

		err := NewRunRepository(mock).Update(context.Background(), run, domain.RunStatusPending)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reconciliation update run") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestRunRepository_FindByID(t *testing.T) {
	id, tenant := uuid.NewString(), uuid.NewString()

	t.Run("reconstitutes run", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_runs WHERE id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(runCols).AddRow(runRow(id, tenant)...))

		run, err := NewRunRepository(mock).FindByID(context.Background(), domain.RunID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if run.ID().String() != id || run.TenantID().String() != tenant || run.Type() != domain.TypePayment || run.Status() != domain.RunStatusCompleted {
			t.Errorf("identity/status not mapped: %v %v %v %v", run.ID(), run.TenantID(), run.Type(), run.Status())
		}
		if !run.PeriodFrom().Equal(testFrom) || !run.PeriodTo().Equal(testTo) || run.PeriodFrom().Location() != time.UTC {
			t.Errorf("period = %v..%v", run.PeriodFrom(), run.PeriodTo())
		}
		if run.TotalRecords() != 10 || run.MatchedCount() != 8 || run.DiscrepancyCount() != 2 || run.ErrorMsg() != "" {
			t.Error("results not mapped")
		}
		if !run.CreatedAt().Equal(testCreated) || run.CompletedAt() == nil {
			t.Error("timestamps not mapped")
		}
	})

	t.Run("no rows maps to ErrRunNotFound", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(id).WillReturnRows(pgxmock.NewRows(runCols))

		_, err := NewRunRepository(mock).FindByID(context.Background(), domain.RunID(id))
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(id).WillReturnError(errBoom)

		_, err := NewRunRepository(mock).FindByID(context.Background(), domain.RunID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "run repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestRunRepository_List(t *testing.T) {
	tenant := uuid.NewString()

	t.Run("filters by tenant", func(t *testing.T) {
		mock := newMock(t)
		id1, id2 := uuid.NewString(), uuid.NewString()
		expectSession(mock, tenant)
		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY created_at DESC LIMIT \\$2").WithArgs(tenant, 5).
			WillReturnRows(pgxmock.NewRows(runCols).AddRow(runRow(id1, tenant)...).AddRow(runRow(id2, tenant)...))
		mock.ExpectCommit()

		runs, err := NewRunRepository(mock).List(context.Background(), domain.TenantID(tenant), 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(runs) != 2 || runs[0].ID().String() != id1 || runs[1].ID().String() != id2 {
			t.Fatalf("unexpected runs: %v", runs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("empty tenant lists all runs", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_runs ORDER BY created_at DESC LIMIT \\$1").WithArgs(20).
			WillReturnRows(pgxmock.NewRows(runCols).AddRow(runRow(uuid.NewString(), "")...))

		runs, err := NewRunRepository(mock).List(context.Background(), "", 20)
		if err != nil || len(runs) != 1 || runs[0].TenantID() != "" {
			t.Fatalf("got %v, %v", runs, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(20).WillReturnError(errBoom)

		_, err := NewRunRepository(mock).List(context.Background(), "", 20)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "run repo: list") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock := newMock(t)
		bad := runRow(uuid.NewString(), tenant)
		bad[6] = "not-a-number"
		expectSession(mock, tenant)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(tenant, 5).WillReturnRows(pgxmock.NewRows(runCols).AddRow(bad...))
		mock.ExpectRollback()

		_, err := NewRunRepository(mock).List(context.Background(), domain.TenantID(tenant), 5)
		if err == nil || !strings.Contains(err.Error(), "run repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenant)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(tenant, 5).
			WillReturnRows(pgxmock.NewRows(runCols).AddRow(runRow(uuid.NewString(), tenant)...).RowError(0, errBoom))
		mock.ExpectRollback()

		_, err := NewRunRepository(mock).List(context.Background(), domain.TenantID(tenant), 5)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── DiscrepancyRepository ─────────────────────────────────────────────────────

var discCols = []string{
	"id", "run_id", "tenant_id", "type", "record_id",
	"system_value", "external_value",
	"status", "notes", "resolved_by",
	"resolved_at", "created_at",
}

func discRow(id, runID string) []any {
	resolved := testCreated.Add(time.Hour)
	return []any{
		id, runID, "", "amount_mismatch", "PSP-1",
		"100 ARS", "90 ARS",
		"resolved", "fixed", "op-1",
		&resolved, testCreated,
	}
}

func TestDiscrepancyRepository_SaveAll(t *testing.T) {
	runID := domain.NewRunID()
	d1 := domain.NewDiscrepancy(domain.NewDiscrepancyID(), runID, "", domain.DiscrepancyMissingInPSP, "A", "{}", "", testCreated)
	d2 := domain.NewDiscrepancy(domain.NewDiscrepancyID(), runID, domain.TenantID(uuid.NewString()), domain.DiscrepancyAmountMismatch, "B", "1 ARS", "2 ARS", testCreated)

	t.Run("inserts each discrepancy", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("INSERT INTO reconciliation_discrepancies").WithArgs(
			d1.ID().String(), runID.String(), (*string)(nil),
			"missing_in_psp", "A", "{}", "", "open", d1.CreatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO reconciliation_discrepancies").WithArgs(
			d2.ID().String(), runID.String(), strPtr(d2.TenantID().String()),
			"amount_mismatch", "B", "1 ARS", "2 ARS", "open", d2.CreatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := NewDiscrepancyRepository(mock).SaveAll(context.Background(), []*domain.Discrepancy{d1, d2}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("empty slice does nothing", func(t *testing.T) {
		mock := newMock(t)
		if err := NewDiscrepancyRepository(mock).SaveAll(context.Background(), nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("stops at first failing insert", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("INSERT INTO reconciliation_discrepancies").WithArgs(anyArgs(9)...).WillReturnError(errBoom)

		err := NewDiscrepancyRepository(mock).SaveAll(context.Background(), []*domain.Discrepancy{d1, d2})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save "+d1.ID().String()) {
			t.Fatalf("expected wrapped errBoom naming d1, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("second insert should not run: %v", err)
		}
	})
}

func TestDiscrepancyRepository_Update(t *testing.T) {
	t.Run("updates resolution columns", func(t *testing.T) {
		mock := newMock(t)
		d := domain.NewDiscrepancy(domain.NewDiscrepancyID(), domain.NewRunID(), "", domain.DiscrepancyAmountMismatch, "A", "", "", testCreated)
		_ = d.Ignore("op-1", "", testCreated)

		mock.ExpectExec("UPDATE reconciliation_discrepancies SET").WithArgs(
			d.ID().String(), "ignored", (*string)(nil), strPtr("op-1"), d.ResolvedAt(),
		).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := NewDiscrepancyRepository(mock).Update(context.Background(), d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		d := domain.NewDiscrepancy(domain.NewDiscrepancyID(), domain.NewRunID(), "", domain.DiscrepancyAmountMismatch, "A", "", "", testCreated)
		mock.ExpectExec("UPDATE reconciliation_discrepancies").WithArgs(anyArgs(5)...).WillReturnError(errBoom)

		err := NewDiscrepancyRepository(mock).Update(context.Background(), d)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reconciliation update discrepancy") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestDiscrepancyRepository_FindByID(t *testing.T) {
	id, runID := uuid.NewString(), uuid.NewString()

	t.Run("reconstitutes discrepancy", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_discrepancies WHERE id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(discCols).AddRow(discRow(id, runID)...))

		d, err := NewDiscrepancyRepository(mock).FindByID(context.Background(), domain.DiscrepancyID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ID().String() != id || d.RunID().String() != runID || d.TenantID() != "" {
			t.Error("identity not mapped")
		}
		if d.Type() != domain.DiscrepancyAmountMismatch || d.RecordID() != "PSP-1" || d.SystemValue() != "100 ARS" || d.ExternalValue() != "90 ARS" {
			t.Error("values not mapped")
		}
		if d.Status() != domain.DiscrepancyResolved || d.Notes() != "fixed" || d.ResolvedBy() != "op-1" || d.ResolvedAt() == nil {
			t.Error("resolution not mapped")
		}
		if !d.CreatedAt().Equal(testCreated) || d.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", d.CreatedAt())
		}
	})

	t.Run("no rows maps to ErrDiscrepancyNotFound", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_discrepancies").WithArgs(id).WillReturnRows(pgxmock.NewRows(discCols))

		_, err := NewDiscrepancyRepository(mock).FindByID(context.Background(), domain.DiscrepancyID(id))
		if !errors.Is(err, domain.ErrDiscrepancyNotFound) {
			t.Fatalf("expected ErrDiscrepancyNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_discrepancies").WithArgs(id).WillReturnError(errBoom)

		_, err := NewDiscrepancyRepository(mock).FindByID(context.Background(), domain.DiscrepancyID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "discrepancy repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestDiscrepancyRepository_ListByRun(t *testing.T) {
	runID := uuid.NewString()

	t.Run("with status filter", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE run_id=\\$1 AND status=\\$2 ORDER BY created_at LIMIT \\$3").WithArgs(runID, "open", 10).
			WillReturnRows(pgxmock.NewRows(discCols).AddRow(discRow(uuid.NewString(), runID)...))

		ds, err := NewDiscrepancyRepository(mock).ListByRun(context.Background(), domain.RunID(runID), "open", 10)
		if err != nil || len(ds) != 1 {
			t.Fatalf("got %v, %v", ds, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("without status filter", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE run_id=\\$1 ORDER BY created_at LIMIT \\$2").WithArgs(runID, 100).
			WillReturnRows(pgxmock.NewRows(discCols).
				AddRow(discRow(uuid.NewString(), runID)...).
				AddRow(discRow(uuid.NewString(), runID)...))

		ds, err := NewDiscrepancyRepository(mock).ListByRun(context.Background(), domain.RunID(runID), "", 100)
		if err != nil || len(ds) != 2 {
			t.Fatalf("got %v, %v", ds, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_discrepancies").WithArgs(runID, 100).WillReturnError(errBoom)

		_, err := NewDiscrepancyRepository(mock).ListByRun(context.Background(), domain.RunID(runID), "", 100)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "discrepancy repo: list") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock := newMock(t)
		bad := discRow(uuid.NewString(), runID)
		bad[11] = "not-a-time"
		mock.ExpectQuery("FROM reconciliation_discrepancies").WithArgs(runID, 100).
			WillReturnRows(pgxmock.NewRows(discCols).AddRow(bad...))

		_, err := NewDiscrepancyRepository(mock).ListByRun(context.Background(), domain.RunID(runID), "", 100)
		if err == nil || !strings.Contains(err.Error(), "discrepancy repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM reconciliation_discrepancies").WithArgs(runID, 100).
			WillReturnRows(pgxmock.NewRows(discCols).AddRow(discRow(uuid.NewString(), runID)...).RowError(0, errBoom))

		_, err := NewDiscrepancyRepository(mock).ListByRun(context.Background(), domain.RunID(runID), "", 100)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── Sesión RLS ────────────────────────────────────────────────────────────────

func TestRepositories_RunInTenantSession(t *testing.T) {
	tenant := uuid.NewString()

	t.Run("run update uses the run tenant", func(t *testing.T) {
		mock := newMock(t)
		run, _ := domain.NewReconciliationRun(domain.NewRunID(), domain.TenantID(tenant), domain.TypePayment, testFrom, testTo, testCreated)
		_ = run.Start()
		expectSession(mock, tenant)
		mock.ExpectExec("UPDATE reconciliation_runs").WithArgs(anyArgs(8)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		if err := NewRunRepository(mock).Update(context.Background(), run, domain.RunStatusPending); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("run lookup uses the caller tenant", func(t *testing.T) {
		mock := newMock(t)
		id := uuid.NewString()
		expectSession(mock, tenant)
		mock.ExpectQuery("FROM reconciliation_runs WHERE id=\\$1").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(runCols).AddRow(runRow(id, tenant)...))
		mock.ExpectCommit()

		if _, err := NewRunRepository(mock).FindByID(tenantCtx(tenant), domain.RunID(id)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("run hidden by RLS is not found", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenant)
		mock.ExpectQuery("FROM reconciliation_runs").WithArgs(pgxmock.AnyArg()).WillReturnRows(pgxmock.NewRows(runCols))
		mock.ExpectRollback()

		if _, err := NewRunRepository(mock).FindByID(tenantCtx(tenant), domain.NewRunID()); !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
	})

	t.Run("discrepancies are saved and updated with their tenant", func(t *testing.T) {
		mock := newMock(t)
		d := domain.NewDiscrepancy(domain.NewDiscrepancyID(), domain.NewRunID(), domain.TenantID(tenant), domain.DiscrepancyMissingInPSP, "A", "{}", "", testCreated)
		expectSession(mock, tenant)
		mock.ExpectExec("INSERT INTO reconciliation_discrepancies").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		expectSession(mock, tenant)
		mock.ExpectExec("UPDATE reconciliation_discrepancies").WithArgs(anyArgs(5)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		repo := NewDiscrepancyRepository(mock)
		if err := repo.SaveAll(context.Background(), []*domain.Discrepancy{d}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := repo.Update(context.Background(), d); err != nil {
			t.Fatalf("update: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("discrepancy lookups use the caller tenant", func(t *testing.T) {
		mock := newMock(t)
		runID := domain.NewRunID()
		expectSession(mock, tenant)
		mock.ExpectQuery("FROM reconciliation_discrepancies WHERE id=\\$1").WithArgs(pgxmock.AnyArg()).WillReturnRows(pgxmock.NewRows(discCols))
		mock.ExpectRollback()
		expectSession(mock, tenant)
		mock.ExpectQuery("WHERE run_id=\\$1 ORDER BY").WithArgs(runID.String(), 10).WillReturnRows(pgxmock.NewRows(discCols))
		mock.ExpectCommit()

		repo := NewDiscrepancyRepository(mock)
		if _, err := repo.FindByID(tenantCtx(tenant), domain.NewDiscrepancyID()); !errors.Is(err, domain.ErrDiscrepancyNotFound) {
			t.Fatalf("expected ErrDiscrepancyNotFound, got %v", err)
		}
		if _, err := repo.ListByRun(tenantCtx(tenant), runID, "", 10); err != nil {
			t.Fatalf("list: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("session setup error is returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(anyArgs(2)...).WillReturnError(errBoom)
		mock.ExpectRollback()

		if _, err := NewRunRepository(mock).List(context.Background(), domain.TenantID(tenant), 5); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
