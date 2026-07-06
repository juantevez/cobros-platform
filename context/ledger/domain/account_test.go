package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewAccount(t *testing.T) {
	t.Run("valid emits AccountCreated", func(t *testing.T) {
		id := NewAccountID()
		tid := NewTenantID_forTest(t)
		acc, err := NewAccount(id, tid, AccountTypeMerchantBalance, "ARS", "saldo del comercio")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if acc.ID() != id || acc.AccountType() != AccountTypeMerchantBalance {
			t.Errorf("fields mismatch: %+v", acc)
		}
		if acc.TenantID() != tid {
			t.Errorf("tenantID = %q, want %q", acc.TenantID(), tid)
		}
		if acc.Currency() != "ARS" || acc.Description() != "saldo del comercio" {
			t.Errorf("currency/description mismatch")
		}
		events := acc.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		created, ok := events[0].(AccountCreatedEvent)
		if !ok {
			t.Fatalf("expected AccountCreatedEvent, got %T", events[0])
		}
		if created.AccountID != id.String() || created.AccountType != "merchant_balance" || created.Currency != "ARS" {
			t.Errorf("event payload mismatch: %+v", created)
		}
	})

	t.Run("invalid currency rejected", func(t *testing.T) {
		_, err := NewAccount(NewAccountID(), NewTenantID_forTest(t), AccountTypeReserve, "XX", "bad")
		if !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
	})
}

func TestReconstituteAccount(t *testing.T) {
	id := NewAccountID()
	tid := NewTenantID_forTest(t)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	acc := ReconstituteAccount(id, tid, AccountTypePlatformFees, "USD", "fees", created)

	if acc.ID() != id || acc.AccountType() != AccountTypePlatformFees || acc.Currency() != "USD" {
		t.Error("fields not restored")
	}
	if !acc.CreatedAt().Equal(created) {
		t.Error("createdAt not restored")
	}
	if len(acc.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func TestAccount_PullEventsClears(t *testing.T) {
	acc, _ := NewAccount(NewAccountID(), NewTenantID_forTest(t), AccountTypeInTransit, "ARS", "")
	if len(acc.PullEvents()) != 1 {
		t.Fatal("expected one event on first pull")
	}
	if len(acc.PullEvents()) != 0 {
		t.Fatal("events should be cleared after pull")
	}
}

// NewTenantID_forTest genera un TenantID válido (UUID) para los tests.
func NewTenantID_forTest(t *testing.T) TenantID {
	t.Helper()
	id, err := ParseTenantID(NewAccountID().String()) // reutiliza un UUID válido
	if err != nil {
		t.Fatalf("build tenant id: %v", err)
	}
	return id
}
