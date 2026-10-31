package onboarding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var accountCols = []string{"account_type", "account_number", "bank_name", "holder_name", "currency"}

// newTxMock devuelve el mock y un contexto con una tx abierta sobre él, como
// el de TxManager.RunInTx: RunWithAppTenant delega en esa tx (las variables
// RLS ya las fijó el TxManager) y las queries llegan al mock igual que sin
// sesión. La aislación real se prueba contra Postgres en rls_test.go.
func newTxMock(t *testing.T) (pgxmock.PgxPoolIface, context.Context) {
	t.Helper()
	mock := newMock(t)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return mock, pkgpostgres.WithTx(context.Background(), tx)
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

func TestBankAccountProvider_GetBankAccount(t *testing.T) {
	tenantID := uuid.NewString()

	t.Run("returns approved bank account", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectQuery(`FROM onboarding_bank_accounts[\s\S]*AND oba\.verified\s+AND oba\.currency = \$2`).WithArgs(tenantID, "ARS").
			WillReturnRows(pgxmock.NewRows(accountCols).
				AddRow("CVU", "0000003100010000000001", "", "Comercio SA", "ARS"))

		got, err := NewBankAccountProvider(mock).GetBankAccount(ctx, domain.TenantID(tenantID), "ars")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := domain.MustBankAccountInfo("CVU", "0000003100010000000001", "", "Comercio SA", "ARS")
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no approved application maps to ErrNoBankAccount", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		mock.ExpectQuery(`FROM onboarding_bank_accounts[\s\S]*AND oba\.verified\s+AND oba\.currency = \$2`).WithArgs(tenantID, "ARS").
			WillReturnRows(pgxmock.NewRows(accountCols))

		_, err := NewBankAccountProvider(mock).GetBankAccount(ctx, domain.TenantID(tenantID), "ars")
		if !errors.Is(err, domain.ErrNoBankAccount) {
			t.Fatalf("expected ErrNoBankAccount, got %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery(`FROM onboarding_bank_accounts[\s\S]*AND oba\.verified\s+AND oba\.currency = \$2`).WithArgs(tenantID, "ARS").WillReturnError(boom)

		_, err := NewBankAccountProvider(mock).GetBankAccount(ctx, domain.TenantID(tenantID), "ars")
		if !errors.Is(err, boom) {
			t.Fatalf("expected boom, got %v", err)
		}
		if !strings.Contains(err.Error(), "bank account provider") {
			t.Errorf("error not wrapped: %v", err)
		}
	})
}
