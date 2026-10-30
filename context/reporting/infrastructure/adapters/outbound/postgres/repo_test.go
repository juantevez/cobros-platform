package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

var errBoom = errors.New("boom")

const tenantA = "11111111-1111-1111-1111-111111111111"

var (
	testFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
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

// expectSession espera la tx efímera que fija app.current_tenant (RLS).
func expectSession(mock pgxmock.PgxPoolIface, tenant string) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs("app.current_tenant", tenant).WillReturnResult(pgxmock.NewResult("SELECT", 1))
}

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

// ── AccountReader ─────────────────────────────────────────────────────────────

func TestAccountReader_AccountType(t *testing.T) {
	t.Run("returns account type", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("SELECT type FROM ledger_accounts WHERE id").WithArgs("acc-1").
			WillReturnRows(pgxmock.NewRows([]string{"type"}).AddRow("merchant_balance"))

		mock.ExpectCommit()
		got, err := NewAccountReader(mock).AccountType(context.Background(), tenantA, "acc-1")
		if err != nil || got != "merchant_balance" {
			t.Fatalf("got %q, %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("unknown account returns empty type without error", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM ledger_accounts").WithArgs("missing").WillReturnRows(pgxmock.NewRows([]string{"type"}))

		mock.ExpectCommit()
		got, err := NewAccountReader(mock).AccountType(context.Background(), tenantA, "missing")
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want empty, nil", got, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM ledger_accounts").WithArgs("acc-1").WillReturnError(errBoom)

		mock.ExpectRollback()
		_, err := NewAccountReader(mock).AccountType(context.Background(), tenantA, "acc-1")
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "account type lookup") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

// ── ProjectionRepository ──────────────────────────────────────────────────────

func TestProjectionRepository_SavePaymentFact(t *testing.T) {
	f := domain.PaymentFact{
		PaymentID: "pay-1", TenantID: tenantA, Currency: "ARS",
		Amount: 10_000, PlatformFee: 300, PSPFee: 150, PaymentMethod: "card", CapturedAt: testFrom,
	}

	t.Run("inserts idempotently by payment_id", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_payment_fact.*ON CONFLICT \\(payment_id\\) DO NOTHING").
			WithArgs("pay-1", tenantA, "ARS", int64(10_000), int64(300), int64(150), "card", testFrom).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		mock.ExpectCommit()
		if err := NewProjectionRepository(mock).SavePaymentFact(context.Background(), f); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("duplicate insert is not an error", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_payment_fact").WithArgs(anyArgs(8)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 0))

		mock.ExpectCommit()
		if err := NewProjectionRepository(mock).SavePaymentFact(context.Background(), f); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_payment_fact").WithArgs(anyArgs(8)...).WillReturnError(errBoom)

		mock.ExpectRollback()
		err := NewProjectionRepository(mock).SavePaymentFact(context.Background(), f)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save payment fact") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestProjectionRepository_SaveLedgerMovement(t *testing.T) {
	m := domain.LedgerMovement{
		EntryID: "entry-1", AccountID: "acc-1", Direction: "credit", TenantID: tenantA,
		AccountType: "merchant_balance", Currency: "ARS", Amount: 500, PostedAt: testFrom,
	}

	t.Run("inserts idempotently by entry, account and direction", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_ledger_movement.*ON CONFLICT \\(entry_id, account_id, direction\\) DO NOTHING").
			WithArgs("entry-1", "acc-1", "credit", tenantA, "merchant_balance", "ARS", int64(500), testFrom).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		mock.ExpectCommit()
		if err := NewProjectionRepository(mock).SaveLedgerMovement(context.Background(), m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_ledger_movement").WithArgs(anyArgs(8)...).WillReturnError(errBoom)

		mock.ExpectRollback()
		err := NewProjectionRepository(mock).SaveLedgerMovement(context.Background(), m)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save ledger movement") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

// ── ReportRepository ──────────────────────────────────────────────────────────

func TestAppendTimeBounds(t *testing.T) {
	cases := []struct {
		name      string
		from, to  time.Time
		wantWhere string
		wantArgs  []any
	}{
		{"no bounds", time.Time{}, time.Time{}, "tenant_id = $1", []any{"t"}},
		{"only from", testFrom, time.Time{}, "tenant_id = $1 AND col >= $2", []any{"t", testFrom}},
		{"only to", time.Time{}, testTo, "tenant_id = $1 AND col < $2", []any{"t", testTo}},
		{"both bounds", testFrom, testTo, "tenant_id = $1 AND col >= $2 AND col < $3", []any{"t", testFrom, testTo}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []any{"t"}
			got := appendTimeBounds("tenant_id = $1", &args, tc.from, tc.to, "col")
			if got != tc.wantWhere {
				t.Errorf("where = %q, want %q", got, tc.wantWhere)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}

	t.Run("placeholders continue after existing args", func(t *testing.T) {
		args := []any{"t", "week"}
		got := appendTimeBounds("tenant_id = $1", &args, testFrom, time.Time{}, "captured_at")
		if got != "tenant_id = $1 AND captured_at >= $3" || len(args) != 3 {
			t.Errorf("where = %q args = %v", got, args)
		}
	})
}

var volumeCols = []string{"bucket", "currency", "payment_count", "gross_amount"}

func TestReportRepository_Volume(t *testing.T) {
	t.Run("aggregates by bucket with granularity and range", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("date_trunc\\(\\$2, captured_at\\).*WHERE tenant_id = \\$1 AND captured_at >= \\$3 AND captured_at < \\$4").
			WithArgs(tenantA, "week", testFrom, testTo).
			WillReturnRows(pgxmock.NewRows(volumeCols).
				AddRow(testFrom, "ARS", int64(3), int64(900)).
				AddRow(testFrom, "USD", int64(1), int64(50)))

		mock.ExpectCommit()
		got, err := NewReportRepository(mock).Volume(context.Background(), application.VolumeQuery{
			TenantID: tenantA, From: testFrom, To: testTo, Granularity: domain.GranularityWeek,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.VolumePoint{
			{Bucket: testFrom, Currency: "ARS", PaymentCount: 3, GrossAmount: 900},
			{Bucket: testFrom, Currency: "USD", PaymentCount: 1, GrossAmount: 50},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("without range only filters by tenant", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("WHERE tenant_id = \\$1\\s+GROUP BY").WithArgs(tenantA, "day").
			WillReturnRows(pgxmock.NewRows(volumeCols))

		mock.ExpectCommit()
		got, err := NewReportRepository(mock).Volume(context.Background(), application.VolumeQuery{TenantID: tenantA, Granularity: domain.GranularityDay})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA, "day").WillReturnError(errBoom)

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Volume(context.Background(), application.VolumeQuery{TenantID: tenantA, Granularity: domain.GranularityDay})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reporting repo: volume") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA, "day").
			WillReturnRows(pgxmock.NewRows(volumeCols).AddRow(testFrom, "ARS", "x", int64(1)))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Volume(context.Background(), application.VolumeQuery{TenantID: tenantA, Granularity: domain.GranularityDay})
		if err == nil || !strings.Contains(err.Error(), "volume scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA, "day").
			WillReturnRows(pgxmock.NewRows(volumeCols).AddRow(testFrom, "ARS", int64(1), int64(1)).RowError(0, errBoom))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Volume(context.Background(), application.VolumeQuery{TenantID: tenantA, Granularity: domain.GranularityDay})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

var revenueCols = []string{"currency", "payment_count", "gross_amount", "platform_fees", "psp_fees",
	"refund_count", "refunded_amount", "chargeback_count", "chargeback_amount"}

func TestReportRepository_Revenue(t *testing.T) {
	t.Run("combines captures and adjustments by currency with net", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		// Cada lado filtra su propia columna de fecha con su propio par de
		// parámetros; el tenant es $1 en ambos.
		mock.ExpectQuery("FROM report_payment_fact\\s+WHERE tenant_id = \\$1 AND captured_at >= \\$2 AND captured_at < \\$3.*" +
			"FROM report_payment_adjustment\\s+WHERE tenant_id = \\$1 AND occurred_at >= \\$4 AND occurred_at < \\$5.*" +
			"FULL OUTER JOIN a USING \\(currency\\)").
			WithArgs(tenantA, testFrom, testTo, testFrom, testTo).
			WillReturnRows(pgxmock.NewRows(revenueCols).
				AddRow("ARS", int64(2), int64(1000), int64(30), int64(15), int64(1), int64(400), int64(1), int64(100)).
				AddRow("USD", int64(0), int64(0), int64(0), int64(0), int64(1), int64(50), int64(0), int64(0)))

		mock.ExpectCommit()
		got, err := NewReportRepository(mock).Revenue(context.Background(), application.RevenueQuery{TenantID: tenantA, From: testFrom, To: testTo})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.RevenueSummary{
			{Currency: "ARS", PaymentCount: 2, GrossAmount: 1000, PlatformFees: 30, PSPFees: 15,
				RefundCount: 1, RefundedAmount: 400, ChargebackCount: 1, ChargebackAmount: 100, NetAmount: 500},
			{Currency: "USD", RefundCount: 1, RefundedAmount: 50, NetAmount: -50},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA).WillReturnError(errBoom)

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Revenue(context.Background(), application.RevenueQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reporting repo: revenue") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA).
			WillReturnRows(pgxmock.NewRows(revenueCols).AddRow("ARS", "x", int64(1), int64(1), int64(1), int64(0), int64(0), int64(0), int64(0)))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Revenue(context.Background(), application.RevenueQuery{TenantID: tenantA})
		if err == nil || !strings.Contains(err.Error(), "revenue scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_payment_fact").WithArgs(tenantA).
			WillReturnRows(pgxmock.NewRows(revenueCols).AddRow("ARS", int64(1), int64(1), int64(1), int64(1), int64(0), int64(0), int64(0), int64(0)).RowError(0, errBoom))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Revenue(context.Background(), application.RevenueQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

var balanceCols = []string{"account_type", "currency", "debits", "credits"}

func TestReportRepository_Balances(t *testing.T) {
	t.Run("computes net as debits minus credits", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_ledger_movement\\s+WHERE tenant_id = \\$1").WithArgs(tenantA).
			WillReturnRows(pgxmock.NewRows(balanceCols).
				AddRow("in_transit", "ARS", int64(1000), int64(0)).
				AddRow("merchant_balance", "ARS", int64(100), int64(700)))

		mock.ExpectCommit()
		got, err := NewReportRepository(mock).Balances(context.Background(), tenantA)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.TenantBalance{
			{AccountType: "in_transit", Currency: "ARS", Debits: 1000, Credits: 0, Net: 1000},
			{AccountType: "merchant_balance", Currency: "ARS", Debits: 100, Credits: 700, Net: -600},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_ledger_movement").WithArgs(tenantA).WillReturnError(errBoom)

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Balances(context.Background(), tenantA)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reporting repo: balances") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_ledger_movement").WithArgs(tenantA).
			WillReturnRows(pgxmock.NewRows(balanceCols).AddRow("x", "ARS", "bad", int64(1)))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Balances(context.Background(), tenantA)
		if err == nil || !strings.Contains(err.Error(), "balances scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectQuery("FROM report_ledger_movement").WithArgs(tenantA).
			WillReturnRows(pgxmock.NewRows(balanceCols).AddRow("x", "ARS", int64(1), int64(1)).RowError(0, errBoom))

		mock.ExpectRollback()
		_, err := NewReportRepository(mock).Balances(context.Background(), tenantA)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── Sesión RLS ────────────────────────────────────────────────────────────────

func TestRepositories_SessionSetupErrorIsReturned(t *testing.T) {
	failingSession := func(t *testing.T) pgxmock.PgxPoolIface {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(anyArgs(2)...).WillReturnError(errBoom)
		mock.ExpectRollback()
		return mock
	}
	ctx := context.Background()
	calls := map[string]func(pgxmock.PgxPoolIface) error{
		"account type": func(m pgxmock.PgxPoolIface) error {
			_, err := NewAccountReader(m).AccountType(ctx, tenantA, "a")
			return err
		},
		"payment fact": func(m pgxmock.PgxPoolIface) error {
			return NewProjectionRepository(m).SavePaymentFact(ctx, domain.PaymentFact{TenantID: tenantA})
		},
		"ledger movement": func(m pgxmock.PgxPoolIface) error {
			return NewProjectionRepository(m).SaveLedgerMovement(ctx, domain.LedgerMovement{TenantID: tenantA})
		},
		"volume": func(m pgxmock.PgxPoolIface) error {
			_, err := NewReportRepository(m).Volume(ctx, application.VolumeQuery{TenantID: tenantA, Granularity: domain.GranularityDay})
			return err
		},
		"revenue": func(m pgxmock.PgxPoolIface) error {
			_, err := NewReportRepository(m).Revenue(ctx, application.RevenueQuery{TenantID: tenantA})
			return err
		},
		"balances": func(m pgxmock.PgxPoolIface) error {
			_, err := NewReportRepository(m).Balances(ctx, tenantA)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(failingSession(t)); !errors.Is(err, errBoom) {
				t.Fatalf("expected errBoom from the session setup, got %v", err)
			}
		})
	}
}

func TestRepositories_WithoutTenantRunWithoutSession(t *testing.T) {
	// Sin tenant no se fija variable: la consulta va directa (solo el
	// operador de plataforma vería filas bajo RLS).
	mock := newMock(t)
	mock.ExpectQuery("FROM report_ledger_movement").WithArgs("").WillReturnRows(pgxmock.NewRows(balanceCols))
	if _, err := NewReportRepository(mock).Balances(context.Background(), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestProjectionRepository_SavePaymentAdjustment(t *testing.T) {
	a := domain.PaymentAdjustment{Kind: domain.AdjustmentChargeback, SourceID: "dis-1", PaymentID: "pay-1",
		TenantID: tenantA, Currency: "ARS", Amount: 700, OccurredAt: testFrom}

	t.Run("inserts idempotently by kind and source", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_payment_adjustment.*ON CONFLICT \\(kind, source_id\\) DO NOTHING").
			WithArgs("chargeback", "dis-1", "pay-1", tenantA, "ARS", int64(700), testFrom).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()

		if err := NewProjectionRepository(mock).SavePaymentAdjustment(context.Background(), a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenantA)
		mock.ExpectExec("INSERT INTO report_payment_adjustment").WithArgs(anyArgs(7)...).WillReturnError(errBoom)
		mock.ExpectRollback()

		err := NewProjectionRepository(mock).SavePaymentAdjustment(context.Background(), a)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save payment adjustment") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
