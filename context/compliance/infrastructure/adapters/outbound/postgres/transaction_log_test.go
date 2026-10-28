package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/compliance/application"
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

func TestTransactionLog_CountSince(t *testing.T) {
	since := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("returns count under RLS session", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM aml_monitored_payments").
			WithArgs("tenant-1", since).WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(7))
		mock.ExpectCommit()

		got, err := log.CountSince(context.Background(), "tenant-1", since)
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
		mock.ExpectQuery("SELECT COUNT").WithArgs("tenant-1", since).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := log.CountSince(context.Background(), "tenant-1", since); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTransactionLog_SumAmountSince(t *testing.T) {
	since := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("returns sum filtered by currency", func(t *testing.T) {
		mock := newMock(t)
		log := NewTransactionLog(mock)
		expectRLSTx(mock, "tenant-1")
		mock.ExpectQuery("SELECT COALESCE\\(SUM\\(amount\\), 0\\) FROM aml_monitored_payments\\s+WHERE tenant_id = \\$1 AND currency = \\$2").
			WithArgs("tenant-1", "ARS", since).WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(int64(9_000)))
		mock.ExpectCommit()

		got, err := log.SumAmountSince(context.Background(), "tenant-1", "ARS", since)
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
		if _, err := log.SumAmountSince(context.Background(), "tenant-1", "ARS", since); err == nil {
			t.Fatal("expected error")
		}
	})
}
