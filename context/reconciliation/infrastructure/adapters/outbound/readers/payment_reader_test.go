package readers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
		mock.ExpectQuery("WHERE created_at >= \\$1 AND created_at < \\$2 AND tenant_id=\\$3 ORDER BY created_at").
			WithArgs(testFrom, testTo, tenant).
			WillReturnRows(pgxmock.NewRows(paymentCols).
				AddRow("pay-1", "PSP-1", int64(100), "ARS", "captured", &captured).
				AddRow("pay-2", "", int64(50), "ARS", "failed", (*time.Time)(nil)))

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

func TestLedgerChecker_CheckBalance(t *testing.T) {
	tenant := uuid.NewString()

	t.Run("returns imbalance for tenant", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM postings p.*AND je.tenant_id=\\$3").WithArgs(testFrom, testTo, tenant).
			WillReturnRows(pgxmock.NewRows([]string{"imbalance"}).AddRow(int64(-250)))

		got, err := NewLedgerChecker(mock).CheckBalance(context.Background(), domain.TenantID(tenant), testFrom, testTo)
		if err != nil || got != -250 {
			t.Fatalf("got %d, %v; want -250", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("global check has no tenant filter", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM postings").WithArgs(testFrom, testTo).
			WillReturnRows(pgxmock.NewRows([]string{"imbalance"}).AddRow(int64(0)))

		got, err := NewLedgerChecker(mock).CheckBalance(context.Background(), "", testFrom, testTo)
		if err != nil || got != 0 {
			t.Fatalf("got %d, %v; want 0", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM postings").WithArgs(testFrom, testTo).WillReturnError(errBoom)

		_, err := NewLedgerChecker(mock).CheckBalance(context.Background(), "", testFrom, testTo)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "ledger checker") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
