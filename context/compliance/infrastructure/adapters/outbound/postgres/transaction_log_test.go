package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

func TestTransactionLog_Record(t *testing.T) {
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	p := application.MonitoredPayment{
		PaymentID: "pay-1", TenantID: "tenant-1", Amount: 1500, Currency: "ARS", CapturedAt: at,
	}

	t.Run("idempotent insert", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		mock.ExpectExec("INSERT INTO aml_monitored_payments .* ON CONFLICT \\(payment_id\\) DO NOTHING").
			WithArgs("pay-1", "tenant-1", int64(1500), "ARS", at).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := log.Record(context.Background(), p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		mock.ExpectExec("INSERT INTO aml_monitored_payments").WillReturnError(errors.New("boom"))
		if err := log.Record(context.Background(), p); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTransactionLog_CountInWindow(t *testing.T) {
	since := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)

	t.Run("returns count of the sliding window under RLS session", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM aml_monitored_payments\\s+WHERE tenant_id = \\$1 AND captured_at > \\$2 AND captured_at <= \\$3").
			WithArgs("tenant-1", since, until).WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(7))
		mock.ExpectCommit()

		got, err := log.CountInWindow(context.Background(), "tenant-1", since, until)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 7 {
			t.Errorf("count = %d, want 7", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COUNT").WithArgs("tenant-1", since, until).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := log.CountInWindow(context.Background(), "tenant-1", since, until); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTransactionLog_SumAmountInWindow(t *testing.T) {
	since := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)

	t.Run("returns sum filtered by currency", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COALESCE\\(SUM\\(amount\\), 0\\) FROM aml_monitored_payments\\s+WHERE tenant_id = \\$1 AND currency = \\$2 AND captured_at > \\$3 AND captured_at <= \\$4").
			WithArgs("tenant-1", "ARS", since, until).WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(int64(9_000)))
		mock.ExpectCommit()

		got, err := log.SumAmountInWindow(context.Background(), "tenant-1", "ARS", since, until)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 9_000 {
			t.Errorf("sum = %d, want 9000", got)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COALESCE").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := log.SumAmountInWindow(context.Background(), "tenant-1", "ARS", since, until); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTransactionLog_PurgeBefore(t *testing.T) {
	before := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deletes a batch as platform operator", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(pkgpostgres.PlatformOperatorKey, "on").
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec(`DELETE FROM aml_monitored_payments\s+WHERE payment_id IN \(\s+SELECT payment_id FROM aml_monitored_payments\s+WHERE captured_at < \$1\s+LIMIT \$2\)`).
			WithArgs(before, 100).WillReturnResult(pgxmock.NewResult("DELETE", 42))
		mock.ExpectCommit()

		n, err := NewTransactionLog(mock).PurgeBefore(context.Background(), before, 100)
		if err != nil || n != 42 {
			t.Fatalf("got %d, %v", n, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec("DELETE FROM aml_monitored_payments").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := NewTransactionLog(mock).PurgeBefore(context.Background(), before, 100); err == nil {
			t.Fatal("expected error")
		}
	})
}
