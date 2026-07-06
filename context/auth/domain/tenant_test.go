package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewTenant(t *testing.T) {
	t.Run("empty legal name is rejected", func(t *testing.T) {
		if _, err := NewTenant(NewTenantID(), ""); !errors.Is(err, ErrEmptyLegalName) {
			t.Fatalf("expected ErrEmptyLegalName, got %v", err)
		}
	})

	t.Run("starts pending in test env and emits TenantCreated", func(t *testing.T) {
		id := NewTenantID()
		tenant, err := NewTenant(id, "Acme SA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tenant.Status() != TenantStatusPending {
			t.Errorf("status = %q, want pending", tenant.Status())
		}
		if !tenant.Environment().IsTest() {
			t.Errorf("environment = %q, want test", tenant.Environment())
		}
		if tenant.IsActive() {
			t.Error("new tenant should not be active")
		}
		events := tenant.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		created, ok := events[0].(TenantCreatedEvent)
		if !ok {
			t.Fatalf("expected TenantCreatedEvent, got %T", events[0])
		}
		if created.TenantID != id.String() || created.LegalName != "Acme SA" {
			t.Errorf("event payload mismatch: %+v", created)
		}
	})
}

func TestTenantActivate(t *testing.T) {
	t.Run("pending to active in production", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		tenant.PullEvents() // descartar el created

		if err := tenant.Activate(EnvironmentProduction); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !tenant.IsActive() {
			t.Error("tenant should be active")
		}
		if !tenant.CanProcessRealPayments() {
			t.Error("active + production tenant should process real payments")
		}
		events := tenant.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if _, ok := events[0].(TenantActivatedEvent); !ok {
			t.Fatalf("expected TenantActivatedEvent, got %T", events[0])
		}
	})

	t.Run("active in test cannot process real payments", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		_ = tenant.Activate(EnvironmentTest)
		if tenant.CanProcessRealPayments() {
			t.Error("active + test tenant must not process real payments")
		}
	})

	t.Run("cannot activate twice", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		_ = tenant.Activate(EnvironmentProduction)
		if err := tenant.Activate(EnvironmentProduction); !errors.Is(err, ErrTenantCannotTransition) {
			t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
		}
	})
}

func TestTenantSuspend(t *testing.T) {
	t.Run("active to suspended", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		_ = tenant.Activate(EnvironmentProduction)
		tenant.PullEvents()

		if err := tenant.Suspend("fraud"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tenant.IsActive() {
			t.Error("suspended tenant should not be active")
		}
		if tenant.CanProcessRealPayments() {
			t.Error("suspended tenant must not process real payments")
		}
		events := tenant.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		suspended, ok := events[0].(TenantSuspendedEvent)
		if !ok {
			t.Fatalf("expected TenantSuspendedEvent, got %T", events[0])
		}
		if suspended.Reason != "fraud" {
			t.Errorf("reason = %q, want fraud", suspended.Reason)
		}
	})

	t.Run("pending can be suspended (KYC rejection)", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		if err := tenant.Suspend("kyc rejected"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("cannot suspend twice", func(t *testing.T) {
		tenant, _ := NewTenant(NewTenantID(), "Acme")
		_ = tenant.Suspend("reason")
		if err := tenant.Suspend("again"); !errors.Is(err, ErrTenantCannotTransition) {
			t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
		}
	})
}

func TestReconstituteTenant(t *testing.T) {
	id := NewTenantID()
	created := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)

	tenant := ReconstituteTenant(id, "Acme SA", TenantStatusActive, EnvironmentProduction, created, updated)

	if tenant.ID() != id || tenant.LegalName() != "Acme SA" {
		t.Error("identity fields not restored")
	}
	if tenant.Status() != TenantStatusActive || !tenant.Environment().IsProd() {
		t.Error("status/environment not restored")
	}
	if !tenant.CreatedAt().Equal(created) || !tenant.UpdatedAt().Equal(updated) {
		t.Error("timestamps not restored")
	}
	if len(tenant.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func TestTenantPullEventsClears(t *testing.T) {
	tenant, _ := NewTenant(NewTenantID(), "Acme")
	if len(tenant.PullEvents()) != 1 {
		t.Fatal("expected one event on first pull")
	}
	if len(tenant.PullEvents()) != 0 {
		t.Fatal("expected events to be cleared after pull")
	}
}
