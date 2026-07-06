package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func TestEntryRepo_SaveAndFindByID(t *testing.T) {
	pool := requireDB(t)
	repo := NewEntryRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	accDebit := seedAccount(t, pool, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accCredit := seedAccount(t, pool, tenantID, domain.AccountTypeInTransit, "ARS")

	entry := buildBalancedEntry(t, tenantID, "entry-key-1", accDebit.ID(), accCredit.ID())
	if err := saveEntryTx(t, pool, tenantID, entry); err != nil {
		t.Fatalf("save entry: %v", err)
	}

	got, err := repo.FindByID(ctx, entry.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID() != entry.ID() || got.IdempotencyKey() != "entry-key-1" {
		t.Errorf("identity mismatch: %+v", got)
	}
	if len(got.Postings()) != 2 {
		t.Fatalf("expected 2 postings, got %d", len(got.Postings()))
	}
	if got.Metadata()["source"] != "integration-test" {
		t.Errorf("metadata not restored: %v", got.Metadata())
	}
	// Verificar que un débito y un crédito de 100 sobrevivieron el round-trip.
	var debits, credits int64
	for _, p := range got.Postings() {
		if p.IsDebit() {
			debits += p.Money().Amount()
		} else {
			credits += p.Money().Amount()
		}
	}
	if debits != 100 || credits != 100 {
		t.Errorf("postings not balanced after round-trip: debits=%d credits=%d", debits, credits)
	}
}

func TestEntryRepo_FindByIdempotencyKey(t *testing.T) {
	pool := requireDB(t)
	repo := NewEntryRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	a1 := seedAccount(t, pool, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	a2 := seedAccount(t, pool, tenantID, domain.AccountTypeInTransit, "ARS")
	entry := buildBalancedEntry(t, tenantID, "idem-key-xyz", a1.ID(), a2.ID())
	if err := saveEntryTx(t, pool, tenantID, entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByIdempotencyKey(ctx, tenantID, "idem-key-xyz")
	if err != nil {
		t.Fatalf("find by key: %v", err)
	}
	if got.ID() != entry.ID() {
		t.Errorf("wrong entry: %s", got.ID())
	}

	// Clave inexistente → ErrEntryNotFound.
	if _, err := repo.FindByIdempotencyKey(ctx, tenantID, "does-not-exist"); !errors.Is(err, domain.ErrEntryNotFound) {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
}

func TestEntryRepo_FindByID_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewEntryRepository(pool)
	if _, err := repo.FindByID(context.Background(), domain.NewEntryID()); !errors.Is(err, domain.ErrEntryNotFound) {
		t.Fatalf("expected ErrEntryNotFound, got %v", err)
	}
}

func TestEntryRepo_DuplicateIdempotencyKey(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()

	tenantID := testTenantID(t)
	a1 := seedAccount(t, pool, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	a2 := seedAccount(t, pool, tenantID, domain.AccountTypeInTransit, "ARS")

	first := buildBalancedEntry(t, tenantID, "dup-key", a1.ID(), a2.ID())
	if err := saveEntryTx(t, pool, tenantID, first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Mismo (tenant, idempotency_key) → viola UNIQUE.
	second := buildBalancedEntry(t, tenantID, "dup-key", a1.ID(), a2.ID())
	if err := saveEntryTx(t, pool, tenantID, second); err == nil {
		t.Fatal("expected unique-violation error on duplicate idempotency key")
	}
	_ = ctx
}
