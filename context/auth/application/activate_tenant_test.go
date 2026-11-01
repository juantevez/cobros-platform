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

// Activar en el ambiente que ya tiene es un no-op: el consumer de onboarding
// puede recibir la aprobación más de una vez.
func TestActivateTenant_AlreadyActiveSameEnvIsNoop(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentTest)
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewActivateTenantUseCase(repo, fakeTx{}, pub)

	if err := uc.Execute(context.Background(), ActivateTenantCmd{TenantID: tenant.ID().String(), Environment: "test"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.published) != 0 || repo.updated != nil {
		t.Errorf("no-op must not update or publish: updated=%v events=%d", repo.updated, len(pub.published))
	}
}

// Antes, un tenant activado a mano en test no podía pasar a producción.
func TestActivateTenant_TestToProduction(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentTest)
	repo := newFakeTenantRepo(tenant)
	pub := &fakePublisher{}
	uc := NewActivateTenantUseCase(repo, fakeTx{}, pub)

	if err := uc.Execute(context.Background(), ActivateTenantCmd{TenantID: tenant.ID().String(), Environment: "production"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tenant.CanProcessRealPayments() {
		t.Error("tenant should be active in production")
	}
	if ev, ok := pub.published[0].(domain.TenantActivatedEvent); !ok || ev.Environment != "production" {
		t.Errorf("event = %+v", pub.published)
	}
}

func TestActivateTenant_ProductionToTestRejected(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	uc := NewActivateTenantUseCase(newFakeTenantRepo(tenant), fakeTx{}, &fakePublisher{})
	err := uc.Execute(context.Background(), ActivateTenantCmd{TenantID: tenant.ID().String(), Environment: "test"})
	if !errors.Is(err, domain.ErrTenantCannotTransition) {
		t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
	}
}

func TestReactivateTenant(t *testing.T) {
	suspended := func(t *testing.T, compliance bool) *domain.Tenant {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		if compliance {
			tenant.SuspendForCompliance()
		} else {
			_ = tenant.Suspend("fraude")
		}
		tenant.PullEvents()
		return tenant
	}
	cases := []struct {
		name                     string
		compliance, byCompliance bool
		want                     error
	}{
		{"operator lifts operator suspension", false, false, nil},
		{"compliance lifts compliance suspension", true, true, nil},
		{"operator cannot lift compliance suspension", true, false, domain.ErrReactivationNotAllowed},
		{"compliance cannot lift operator suspension", false, true, domain.ErrReactivationNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := suspended(t, tc.compliance)
			pub := &fakePublisher{}
			uc := NewReactivateTenantUseCase(newFakeTenantRepo(tenant), fakeTx{}, pub)
			err := uc.Execute(context.Background(), ReactivateTenantCmd{TenantID: tenant.ID().String(), ByCompliance: tc.byCompliance})
			if !errors.Is(err, tc.want) && !(err == nil && tc.want == nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if tenant.Status() != domain.TenantStatusSuspended || len(pub.published) != 0 {
					t.Error("rejected reactivation must not change the tenant")
				}
				return
			}
			if !tenant.CanProcessRealPayments() || tenant.SuspendedForCompliance() {
				t.Errorf("status=%s env=%s compliance=%v", tenant.Status(), tenant.Environment(), tenant.SuspendedForCompliance())
			}
			if ev, ok := pub.published[0].(domain.TenantReactivatedEvent); !ok || ev.Environment != "production" {
				t.Errorf("event = %+v", pub.published)
			}
		})
	}

	t.Run("active tenant cannot be reactivated", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		uc := NewReactivateTenantUseCase(newFakeTenantRepo(tenant), fakeTx{}, &fakePublisher{})
		if err := uc.Execute(context.Background(), ReactivateTenantCmd{TenantID: tenant.ID().String()}); !errors.Is(err, domain.ErrTenantCannotTransition) {
			t.Fatalf("err = %v", err)
		}
	})
}
