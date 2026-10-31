package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var errBoom = errors.New("boom")

var payoutCols = []string{
	"id", "tenant_id", "amount", "currency",
	"bank_acct_type", "bank_acct_num", "bank_name", "holder_name",
	"status", "bank_reference", "failure_reason", "ledger_entry_key",
	"idempotency_key",
	"initiated_at", "confirmed_at", "failed_at",
	"created_at", "updated_at",
}

var testTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

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

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

func strPtr(s string) *string { return &s }

func testBankAccount() domain.BankAccountInfo {
	return domain.MustBankAccountInfo("CBU", "0170099220000067797370", "Banco Test", "Comercio SA", "ARS")
}

func newPayout(t *testing.T) *domain.Payout {
	t.Helper()
	money, _ := domain.NewMoney(5000, "ARS")
	p, err := domain.NewPayout(domain.NewPayoutID(), domain.TenantID(uuid.NewString()), money, testBankAccount(), "idem-key-1")
	if err != nil {
		t.Fatalf("NewPayout: %v", err)
	}
	return p
}

// confirmedRow arma una fila de un payout confirmado (failed_at NULL).
func confirmedRow(id, tenantID string) []any {
	confirmed := testTime.Add(time.Minute)
	return []any{
		id, tenantID, int64(5000), "ARS",
		"CBU", "0170099220000067797370", "Banco Test", "Comercio SA",
		"confirmed", "BANK-1", "", "payout_initiated_" + id,
		"idem-key-1",
		&testTime, &confirmed, (*time.Time)(nil),
		testTime, confirmed,
	}
}

func TestPayoutRepository_Save(t *testing.T) {
	t.Run("inserts all columns with NULL for empty optional strings", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)
		p := newPayout(t)

		mock.ExpectExec("INSERT INTO payouts").WithArgs(
			p.ID().String(), p.TenantID().String(),
			int64(5000), "ARS",
			"CBU", "0170099220000067797370", strPtr("Banco Test"), "Comercio SA",
			"initiated", (*string)(nil), (*string)(nil),
			p.LedgerEntryKey(), p.IdempotencyKey(),
			(*time.Time)(nil), (*time.Time)(nil), (*time.Time)(nil),
			p.CreatedAt(), p.UpdatedAt(),
		).WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(ctx, p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectExec("INSERT INTO payouts").WithArgs(anyArgs(18)...).WillReturnError(errBoom)

		err := repo.Save(ctx, newPayout(t))
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "payout repo: save") {
			t.Errorf("error not wrapped: %v", err)
		}
	})
}

func TestPayoutRepository_Update(t *testing.T) {
	t.Run("updates mutable columns", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)
		p := newPayout(t)
		_ = p.MarkProcessing()
		_ = p.Confirm("BANK-9")

		mock.ExpectExec("UPDATE payouts SET").WithArgs(
			p.ID().String(),
			"confirmed", strPtr("BANK-9"), (*string)(nil),
			p.InitiatedAt(), p.ConfirmedAt(), (*time.Time)(nil), p.UpdatedAt(),
			"processing",
		).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.Update(ctx, p, domain.StatusProcessing); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectExec("UPDATE payouts SET").WithArgs(anyArgs(9)...).WillReturnError(errBoom)

		err := repo.Update(ctx, newPayout(t), domain.StatusInitiated)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payout repo: update") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestPayoutRepository_FindByID(t *testing.T) {
	id := uuid.NewString()
	tenantID := uuid.NewString()

	t.Run("reconstitutes payout from row", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts WHERE id=").WithArgs(id).
			WillReturnRows(pgxmock.NewRows(payoutCols).AddRow(confirmedRow(id, tenantID)...))

		p, err := repo.FindByID(ctx, domain.PayoutID(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID().String() != id || p.TenantID().String() != tenantID {
			t.Errorf("ids = %s/%s", p.ID(), p.TenantID())
		}
		if p.Amount().Amount() != 5000 || p.Amount().Currency() != "ARS" {
			t.Errorf("amount = %v", p.Amount())
		}
		if p.BankAccount() != testBankAccount() {
			t.Errorf("bank account = %+v", p.BankAccount())
		}
		if p.Status() != domain.StatusConfirmed || p.BankReference() != "BANK-1" || p.FailureReason() != "" {
			t.Errorf("state = %s ref=%q reason=%q", p.Status(), p.BankReference(), p.FailureReason())
		}
		if p.LedgerEntryKey() != "payout_initiated_"+id {
			t.Errorf("ledger key = %q", p.LedgerEntryKey())
		}
		if p.InitiatedAt() == nil || p.ConfirmedAt() == nil || p.FailedAt() != nil {
			t.Error("nullable timestamps not mapped")
		}
		if !p.CreatedAt().Equal(testTime) || p.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt = %v", p.CreatedAt())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no rows maps to ErrPayoutNotFound", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts WHERE id=").WithArgs(id).WillReturnRows(pgxmock.NewRows(payoutCols))

		_, err := repo.FindByID(ctx, domain.PayoutID(id))
		if !errors.Is(err, domain.ErrPayoutNotFound) {
			t.Fatalf("expected ErrPayoutNotFound, got %v", err)
		}
	})

	t.Run("query error is wrapped as scan error", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts WHERE id=").WithArgs(id).WillReturnError(errBoom)

		_, err := repo.FindByID(ctx, domain.PayoutID(id))
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payout repo: scan") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestPayoutRepository_ListByTenant(t *testing.T) {
	tenantID := uuid.NewString()

	t.Run("returns payouts in row order", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)
		id1, id2 := uuid.NewString(), uuid.NewString()

		mock.ExpectQuery("WHERE tenant_id=\\$1 ORDER BY created_at DESC LIMIT \\$2").WithArgs(tenantID, 10).
			WillReturnRows(pgxmock.NewRows(payoutCols).
				AddRow(confirmedRow(id1, tenantID)...).
				AddRow(confirmedRow(id2, tenantID)...))

		got, err := repo.ListByTenant(ctx, domain.TenantID(tenantID), 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].ID().String() != id1 || got[1].ID().String() != id2 {
			t.Fatalf("unexpected payouts: %v", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("no rows returns empty slice", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts").WithArgs(tenantID, 5).WillReturnRows(pgxmock.NewRows(payoutCols))

		got, err := repo.ListByTenant(ctx, domain.TenantID(tenantID), 5)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts").WithArgs(tenantID, 5).WillReturnError(errBoom)

		_, err := repo.ListByTenant(ctx, domain.TenantID(tenantID), 5)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "payout repo: list") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("scan error aborts listing", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)
		bad := confirmedRow(uuid.NewString(), tenantID)
		bad[2] = "not-a-number" // amount

		mock.ExpectQuery("FROM payouts").WithArgs(tenantID, 5).
			WillReturnRows(pgxmock.NewRows(payoutCols).AddRow(bad...))

		_, err := repo.ListByTenant(ctx, domain.TenantID(tenantID), 5)
		if err == nil || !strings.Contains(err.Error(), "payout repo: scan") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("rows error is returned", func(t *testing.T) {
		mock, ctx := newTxMock(t)
		repo := NewPayoutRepository(mock)

		mock.ExpectQuery("FROM payouts").WithArgs(tenantID, 5).
			WillReturnRows(pgxmock.NewRows(payoutCols).
				AddRow(confirmedRow(uuid.NewString(), tenantID)...).
				RowError(0, errBoom))

		_, err := repo.ListByTenant(ctx, domain.TenantID(tenantID), 5)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestNullStr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty string should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("nullStr(\"x\") = %v", got)
	}
}

// El request original y el recuperador pueden resolver el mismo payout: el
// UPDATE exige el estado de partida y el que llega tarde no escribe.
func TestPayoutRepository_Update_Changed(t *testing.T) {
	mock, ctx := newTxMock(t)
	mock.ExpectExec(`UPDATE payouts SET[\s\S]*WHERE id=\$1 AND status=\$9`).WithArgs(anyArgs(9)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := NewPayoutRepository(mock).Update(ctx, newPayout(t), domain.StatusProcessing); !errors.Is(err, domain.ErrPayoutChanged) {
		t.Fatalf("expected ErrPayoutChanged, got %v", err)
	}
}

func TestPayoutRepository_ListUnresolved(t *testing.T) {
	mock, ctx := newTxMock(t)
	cut := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM payouts WHERE status IN \('processing','unknown'\) AND updated_at < \$1 ORDER BY updated_at LIMIT \$2`).
		WithArgs(cut, 50).WillReturnRows(pgxmock.NewRows(payoutCols))
	if _, err := NewPayoutRepository(mock).ListUnresolved(ctx, cut, 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
