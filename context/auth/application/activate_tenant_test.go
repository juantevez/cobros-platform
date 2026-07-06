package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestActivateTenant_Success(t *testing.T) {
	tenant := newPendingTenant(t)
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewActivateTenantUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), ActivateTenantCmd{
		TenantID:    tenant.ID().String(),
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || repo.updated.Status() != domain.TenantStatusActive {
		t.Fatal("tenant was not updated to active")
	}
	if !repo.updated.Environment().IsProd() {
		t.Error("environment should be production")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.TenantActivatedEvent); !ok {
		t.Fatalf("expected TenantActivatedEvent, got %T", pub.published[0])
	}
}

func TestActivateTenant_InvalidInputs(t *testing.T) {
	repo := newFakeTenantRepo()
	uc := NewActivateTenantUseCase(repo, fakeTx{}, &fakePublisher{})

	t.Run("invalid tenant id", func(t *testing.T) {
		err := uc.Execute(context.Background(), ActivateTenantCmd{TenantID: "nope", Environment: "test"})
		if !errors.Is(err, domain.ErrInvalidID) {
			t.Fatalf("expected ErrInvalidID, got %v", err)
		}
	})

	t.Run("invalid environment", func(t *testing.T) {
		err := uc.Execute(context.Background(), ActivateTenantCmd{TenantID: domain.NewTenantID().String(), Environment: "staging"})
		if !errors.Is(err, domain.ErrInvalidEnvironment) {
			t.Fatalf("expected ErrInvalidEnvironment, got %v", err)
		}
	})
}

func TestActivateTenant_NotFound(t *testing.T) {
	repo := newFakeTenantRepo()
	uc := NewActivateTenantUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), ActivateTenantCmd{
		TenantID:    domain.NewTenantID().String(),
		Environment: "test",
	})
	if !errors.Is(err, domain.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestActivateTenant_AlreadyActive(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentTest)
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewActivateTenantUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), ActivateTenantCmd{
		TenantID:    tenant.ID().String(),
		Environment: "test",
	})
	if !errors.Is(err, domain.ErrTenantCannotTransition) {
		t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
	}
	if len(pub.published) != 0 {
		t.Error("no events should be published on failed transition")
	}
}
