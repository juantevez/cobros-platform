package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestSuspendTenant_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewSuspendTenantUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), SuspendTenantCmd{
		TenantID: tenant.ID().String(),
		Reason:   "fraud detected",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || repo.updated.Status() != domain.TenantStatusSuspended {
		t.Fatal("tenant was not suspended")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	suspended, ok := pub.published[0].(domain.TenantSuspendedEvent)
	if !ok {
		t.Fatalf("expected TenantSuspendedEvent, got %T", pub.published[0])
	}
	if suspended.Reason != "fraud detected" {
		t.Errorf("reason = %q, want 'fraud detected'", suspended.Reason)
	}
}

func TestSuspendTenant_EmptyReason(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	uc := NewSuspendTenantUseCase(newFakeTenantRepo(tenant), fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), SuspendTenantCmd{TenantID: tenant.ID().String(), Reason: ""})
	if err == nil {
		t.Fatal("expected error for empty reason")
	}
}

func TestSuspendTenant_InvalidTenantID(t *testing.T) {
	uc := NewSuspendTenantUseCase(newFakeTenantRepo(), fakeTx{}, &fakePublisher{})
	err := uc.Execute(context.Background(), SuspendTenantCmd{TenantID: "nope", Reason: "x"})
	if !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestSuspendTenant_NotFound(t *testing.T) {
	uc := NewSuspendTenantUseCase(newFakeTenantRepo(), fakeTx{}, &fakePublisher{})
	err := uc.Execute(context.Background(), SuspendTenantCmd{
		TenantID: domain.NewTenantID().String(), Reason: "x",
	})
	if !errors.Is(err, domain.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestSuspendTenant_AlreadySuspended(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	_ = tenant.Suspend("first")
	tenant.PullEvents()
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewSuspendTenantUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), SuspendTenantCmd{TenantID: tenant.ID().String(), Reason: "again"})
	if !errors.Is(err, domain.ErrTenantCannotTransition) {
		t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
	}
	if len(pub.published) != 0 {
		t.Error("no events should be published on failed transition")
	}
}
