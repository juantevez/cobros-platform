package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// applyPostings construye un asiento balanceado (debit acc1 / credit acc2) y
// devuelve sus postings, sin persistir el asiento.
func balancedPostings(t *testing.T, tenantID domain.TenantID, accDebit, accCredit domain.AccountID, amount int64) []domain.Posting {
	t.Helper()
	e, err := domain.NewJournalEntry(
		domain.NewEntryID(), tenantID, "k", "d", nil, time.Now().UTC(),
		[]domain.PostingInput{
			{AccountID: accDebit, Direction: domain.DirectionDebit, Amount: amount, Currency: "ARS"},
			{AccountID: accCredit, Direction: domain.DirectionCredit, Amount: amount, Currency: "ARS"},
		},
	)
	if err != nil {
		t.Fatalf("build postings: %v", err)
	}
	return e.Postings()
}

func TestBalanceRepo_ApplyAndGet(t *testing.T) {
	pool := requireDB(t)
	repo := NewBalanceRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	accD := seedAccount(t, pool, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accC := seedAccount(t, pool, tenantID, domain.AccountTypeInTransit, "ARS")

	// Débito -100 a accD, crédito +100 a accC.
	if err := repo.Apply(ctx, balancedPostings(t, tenantID, accD.ID(), accC.ID(), 100)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	dBal, _ := repo.GetBalance(ctx, accD.ID())
	cBal, _ := repo.GetBalance(ctx, accC.ID())
	if dBal != -100 {
		t.Errorf("debit account balance = %d, want -100", dBal)
	}
	if cBal != 100 {
		t.Errorf("credit account balance = %d, want 100", cBal)
	}

	// Aplicar de nuevo acumula: débito -50 más.
	if err := repo.Apply(ctx, balancedPostings(t, tenantID, accD.ID(), accC.ID(), 50)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	dBal2, _ := repo.GetBalance(ctx, accD.ID())
	if dBal2 != -150 {
		t.Errorf("accumulated debit balance = %d, want -150", dBal2)
	}
}

func TestBalanceRepo_ApplyUnknownAccount(t *testing.T) {
	pool := requireDB(t)
	repo := NewBalanceRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	// Ambas cuentas inexistentes en account_balances → rowsAffected 0 → error.
	postings := balancedPostings(t, tenantID, domain.NewAccountID(), domain.NewAccountID(), 100)
	if err := repo.Apply(ctx, postings); err == nil {
		t.Fatal("expected error applying to an account not in account_balances")
	}
}

func TestBalanceRepo_GetBalanceUnknownAccount(t *testing.T) {
	pool := requireDB(t)
	repo := NewBalanceRepository(pool)
	if _, err := repo.GetBalance(context.Background(), domain.NewAccountID()); err == nil {
		t.Fatal("expected error getting balance of an unknown account")
	}
}
