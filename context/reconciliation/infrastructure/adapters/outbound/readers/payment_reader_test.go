package readers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

var errBoom = errors.New("boom")

var (
	testFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

var paymentCols = []string{"id", "psp_reference", "amount", "currency", "status", "captured_at"}

// expectSession espera la tx efímera que fija app.current_tenant (RLS).
func expectSession(mock pgxmock.PgxPoolIface, tenant string) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs("app.current_tenant", tenant).WillReturnResult(pgxmock.NewResult("SELECT", 1))
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

func TestPaymentReader_ReadByPeriod(t *testing.T) {
	tenant := uuid.NewString()
	captured := testFrom.Add(time.Hour)

	t.Run("filters by tenant and maps rows", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenant)
		mock.ExpectQuery("WHERE created_at >= \\$1 AND created_at < \\$2 AND tenant_id=\\$3 ORDER BY created_at").
			WithArgs(testFrom, testTo, tenant).
			WillReturnRows(pgxmock.NewRows(paymentCols).
				AddRow("pay-1", "PSP-1", int64(100), "ARS", "captured", &captured).
				AddRow("pay-2", "", int64(50), "ARS", "failed", (*time.Time)(nil)))
		mock.ExpectCommit()

		got, err := NewPaymentReader(mock).ReadByPeriod(context.Background(), domain.TenantID(tenant), testFrom, testTo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d payments, want 2", len(got))
		}
		want0 := application.SystemPayment{PaymentID: "pay-1", PSPReference: "PSP-1", Amount: 100, Currency: "ARS", Status: "captured", CapturedAt: got[0].CapturedAt}
		if got[0] != want0 || got[0].CapturedAt == nil || !got[0].CapturedAt.Equal(captured) {
			t.Errorf("payment 0 = %+v", got[0])
		}
		if got[1].PSPReference != "" || got[1].CapturedAt != nil {
			t.Errorf("payment 1 = %+v", got[1])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("global read has no tenant filter", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("WHERE created_at >= \\$1 AND created_at < \\$2 ORDER BY created_at").
			WithArgs(testFrom, testTo).
			WillReturnRows(pgxmock.NewRows(paymentCols))

		got, err := NewPaymentReader(mock).ReadByPeriod(context.Background(), "", testFrom, testTo)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM payments").WithArgs(testFrom, testTo).WillReturnError(errBoom)

		_, err := NewPaymentReader(mock).ReadByPeriod(context.Background(), "", testFrom, testTo)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payment reader: query") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM payments").WithArgs(testFrom, testTo).
			WillReturnRows(pgxmock.NewRows(paymentCols).AddRow("pay-1", "PSP-1", "not-a-number", "ARS", "captured", (*time.Time)(nil)))

		_, err := NewPaymentReader(mock).ReadByPeriod(context.Background(), "", testFrom, testTo)
		if err == nil || !strings.Contains(err.Error(), "payment reader: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM payments").WithArgs(testFrom, testTo).
			WillReturnRows(pgxmock.NewRows(paymentCols).
				AddRow("pay-1", "PSP-1", int64(1), "ARS", "captured", (*time.Time)(nil)).
				RowError(0, errBoom))

		_, err := NewPaymentReader(mock).ReadByPeriod(context.Background(), "", testFrom, testTo)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// expectLedgerQueries espera las cuatro consultas del chequeo con los
// argumentos dados (período y, si corresponde, tenant).
func expectLedgerQueries(mock pgxmock.PgxPoolIface, args []any, unbalanced, missing *pgxmock.Rows) {
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM journal_entries je WHERE je.created_at >= \\$1 AND je.created_at < \\$2").
		WithArgs(args...).WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectQuery("LEFT JOIN postings p ON p.entry_id = je.id.*GROUP BY je.id, p.currency.*HAVING").
		WithArgs(args...).WillReturnRows(unbalanced)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM payments p WHERE p.captured_at >= \\$1 AND p.captured_at < \\$2.*status IN \\('captured', 'refunded'\\)").
		WithArgs(args...).WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery("NOT EXISTS.*idempotency_key = 'payment_captured_' \\|\\| p.id::text").
		WithArgs(args...).WillReturnRows(missing)
}

var (
	unbalancedCols = []string{"id", "currency", "imbalance"}
	missingCols    = []string{"id", "psp_reference", "amount", "currency", "status", "captured_at"}
)

func TestLedgerChecker_Check(t *testing.T) {
	tenant := uuid.NewString()
	captured := testFrom.Add(time.Hour)

	t.Run("collects unbalanced entries and captures without entry for the tenant", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock, tenant)
		expectLedgerQueries(mock, []any{testFrom, testTo, tenant},
			pgxmock.NewRows(unbalancedCols).AddRow("E1", "ARS", int64(50)).AddRow("E2", "", int64(0)),
			pgxmock.NewRows(missingCols).AddRow("P2", "R2", int64(200), "ARS", "captured", &captured))
		mock.ExpectCommit()

		got, err := NewLedgerChecker(mock).Check(context.Background(), domain.TenantID(tenant), testFrom, testTo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.EntriesChecked != 4 || got.CapturesChecked != 3 {
			t.Errorf("checked entries=%d captures=%d, want 4/3", got.EntriesChecked, got.CapturesChecked)
		}
		wantUnbalanced := []application.LedgerImbalance{{EntryID: "E1", Currency: "ARS", Imbalance: 50}, {EntryID: "E2"}}
		if len(got.Unbalanced) != 2 || got.Unbalanced[0] != wantUnbalanced[0] || got.Unbalanced[1] != wantUnbalanced[1] {
			t.Errorf("unbalanced = %+v", got.Unbalanced)
		}
		if len(got.MissingEntries) != 1 || got.MissingEntries[0].PaymentID != "P2" || !got.MissingEntries[0].CapturedAt.Equal(captured) {
			t.Errorf("missing = %+v", got.MissingEntries)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("global check has no tenant filter", func(t *testing.T) {
		mock := newMock(t)
		expectLedgerQueries(mock, []any{testFrom, testTo}, pgxmock.NewRows(unbalancedCols), pgxmock.NewRows(missingCols))

		got, err := NewLedgerChecker(mock).Check(context.Background(), "", testFrom, testTo)
		if err != nil || len(got.Unbalanced) != 0 || len(got.MissingEntries) != 0 {
			t.Fatalf("got %+v, %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	args := []any{testFrom, testTo}
	countRow := func() *pgxmock.Rows { return pgxmock.NewRows([]string{"count"}).AddRow(1) }
	failures := []struct {
		name  string
		setup func(pgxmock.PgxPoolIface)
		want  string
	}{
		{"count entries", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnError(errBoom)
		}, "count entries"},
		{"unbalanced query", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnError(errBoom)
		}, "unbalanced entries"},
		{"unbalanced scan", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols).AddRow("E", "ARS", "x"))
		}, "scan entry"},
		{"unbalanced rows", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols).AddRow("E", "ARS", int64(1)).CloseError(errBoom))
		}, "unbalanced entries"},
		{"count captures", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols))
			m.ExpectQuery("FROM payments").WithArgs(args...).WillReturnError(errBoom)
		}, "count captures"},
		{"missing query", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols))
			m.ExpectQuery("FROM payments").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("NOT EXISTS").WithArgs(args...).WillReturnError(errBoom)
		}, "captures without entry"},
		{"missing scan", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols))
			m.ExpectQuery("FROM payments").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("NOT EXISTS").WithArgs(args...).WillReturnRows(pgxmock.NewRows(missingCols).AddRow("P", "", "x", "ARS", "captured", &captured))
		}, "scan capture"},
		{"missing rows", func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery("FROM journal_entries").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("LEFT JOIN postings").WithArgs(args...).WillReturnRows(pgxmock.NewRows(unbalancedCols))
			m.ExpectQuery("FROM payments").WithArgs(args...).WillReturnRows(countRow())
			m.ExpectQuery("NOT EXISTS").WithArgs(args...).WillReturnRows(pgxmock.NewRows(missingCols).AddRow("P", "", int64(1), "ARS", "captured", &captured).CloseError(errBoom))
		}, "captures without entry"},
	}
	for _, tc := range failures {
		t.Run(tc.name+" error is wrapped", func(t *testing.T) {
			mock := newMock(t)
			tc.setup(mock)
			_, err := NewLedgerChecker(mock).Check(context.Background(), "", testFrom, testTo)
			if err == nil || !strings.Contains(err.Error(), "ledger checker: "+tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestReaders_SessionSetupError(t *testing.T) {
	tenant := uuid.NewString()
	failingSession := func(t *testing.T) pgxmock.PgxPoolIface {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(anyArgs(2)...).WillReturnError(errBoom)
		mock.ExpectRollback()
		return mock
	}

	if _, err := NewPaymentReader(failingSession(t)).ReadByPeriod(context.Background(), domain.TenantID(tenant), testFrom, testTo); !errors.Is(err, errBoom) {
		t.Errorf("payment reader: expected errBoom, got %v", err)
	}
	if _, err := NewLedgerChecker(failingSession(t)).Check(context.Background(), domain.TenantID(tenant), testFrom, testTo); !errors.Is(err, errBoom) {
		t.Errorf("ledger checker: expected errBoom, got %v", err)
	}
}

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}
