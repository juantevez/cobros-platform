package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payout/domain"
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

func TestBalanceChecker_GetAvailableBalance(t *testing.T) {
	tenantID := uuid.NewString()

	t.Run("returns merchant balance for tenant and currency", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM account_balances").WithArgs(tenantID, "ARS").
			WillReturnRows(pgxmock.NewRows([]string{"balance"}).AddRow(int64(12_345)))

		got, err := NewBalanceChecker(mock).GetAvailableBalance(context.Background(), domain.TenantID(tenantID), "ARS")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 12_345 {
			t.Errorf("balance = %d, want 12345", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("negative balance returned as is", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM account_balances").WithArgs(tenantID, "ARS").
			WillReturnRows(pgxmock.NewRows([]string{"balance"}).AddRow(int64(-50)))

		got, err := NewBalanceChecker(mock).GetAvailableBalance(context.Background(), domain.TenantID(tenantID), "ARS")
		if err != nil || got != -50 {
			t.Fatalf("got %d, %v; want -50, nil", got, err)
		}
	})

	t.Run("missing account is wrapped error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("FROM account_balances").WithArgs(tenantID, "USD").
			WillReturnRows(pgxmock.NewRows([]string{"balance"}))

		_, err := NewBalanceChecker(mock).GetAvailableBalance(context.Background(), domain.TenantID(tenantID), "USD")
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected wrapped ErrNoRows, got %v", err)
		}
		if !strings.Contains(err.Error(), "balance checker") {
			t.Errorf("error not wrapped: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock := newMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM account_balances").WithArgs(tenantID, "ARS").WillReturnError(boom)

		_, err := NewBalanceChecker(mock).GetAvailableBalance(context.Background(), domain.TenantID(tenantID), "ARS")
		if !errors.Is(err, boom) {
			t.Fatalf("expected boom, got %v", err)
		}
	})
}
