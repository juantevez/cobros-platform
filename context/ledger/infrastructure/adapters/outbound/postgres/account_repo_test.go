package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func TestAccountRepo_SaveAndFindByID(t *testing.T) {
	pool := requireDB(t)
	repo := NewAccountRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	acc, _ := domain.NewAccount(domain.NewAccountID(), tenantID, domain.AccountTypeMerchantBalance, "ARS", "saldo")
	acc.PullEvents()

	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByID(ctx, acc.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID() != acc.ID() || got.AccountType() != domain.AccountTypeMerchantBalance || got.Currency() != "ARS" {
		t.Errorf("mismatch: %+v", got)
	}
	timesClose(t, got.CreatedAt(), acc.CreatedAt())

	// El saldo se inicializó en cero.
	bal, err := NewBalanceRepository(pool).GetBalance(ctx, acc.ID())
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal != 0 {
		t.Errorf("initial balance = %d, want 0", bal)
	}
}

func TestAccountRepo_FindByTenantAndType(t *testing.T) {
	pool := requireDB(t)
	repo := NewAccountRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	acc := seedAccount(t, pool, tenantID, domain.AccountTypeReserve, "ARS")

	got, err := repo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeReserve, "ARS")
	if err != nil {
		t.Fatalf("find by type: %v", err)
	}
	if got.ID() != acc.ID() {
		t.Errorf("wrong account: %s", got.ID())
	}

	// Otra moneda no debe encontrarse.
	if _, err := repo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeReserve, "USD"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound for USD, got %v", err)
	}
}

func TestAccountRepo_FindByID_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewAccountRepository(pool)
	if _, err := repo.FindByID(context.Background(), domain.NewAccountID()); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestAccountRepo_DuplicateTypeCurrency(t *testing.T) {
	pool := requireDB(t)
	repo := NewAccountRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	seedAccount(t, pool, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	// El índice único (tenant, type, currency) rechaza el duplicado.
	dup, _ := domain.NewAccount(domain.NewAccountID(), tenantID, domain.AccountTypeMerchantBalance, "ARS", "otra")
	dup.PullEvents()
	if err := repo.Save(ctx, dup); err == nil {
		t.Fatal("expected unique-violation error for duplicate (tenant, type, currency)")
	}
}
