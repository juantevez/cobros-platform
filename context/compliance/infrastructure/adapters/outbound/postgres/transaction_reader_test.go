package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func TestTransactionReader_CountCapturedSince(t *testing.T) {
	since := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("returns count", func(t *testing.T) {
		mock := newMock(t)
		reader := NewTransactionReader(mock)
		rows := pgxmock.NewRows([]string{"count"}).AddRow(7)
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM payments").
			WithArgs("tenant-1", since).WillReturnRows(rows)

		got, err := reader.CountCapturedSince(context.Background(), "tenant-1", since)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 7 {
			t.Errorf("count = %d, want 7", got)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		reader := NewTransactionReader(mock)
		mock.ExpectQuery("SELECT COUNT").WithArgs("tenant-1", since).WillReturnError(errors.New("boom"))
		if _, err := reader.CountCapturedSince(context.Background(), "tenant-1", since); err == nil {
			t.Fatal("expected error")
		}
	})
}
