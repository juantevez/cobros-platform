package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestProvisionPlatformUser(t *testing.T) {
	newUC := func(tenant *domain.Tenant, members *fakeMembershipRepo) *ProvisionPlatformUserUseCase {
		return NewProvisionPlatformUserUseCase(newFakeTenantRepo(tenant), newFakeUserRepo(), members,
			&fakeHasher{}, fakeTx{}, &fakePublisher{})
	}

	for _, role := range []domain.Role{domain.RoleComplianceOfficer, domain.RolePlatformSupport} {
		t.Run("creates "+role.String(), func(t *testing.T) {
			tenant := newActiveTenant(t, domain.EnvironmentProduction)
			members := newFakeMembershipRepo()
			res, err := newUC(tenant, members).Execute(context.Background(), RegisterUserCmd{
				TenantID: tenant.ID().String(), Email: "ana@plataforma.com", Password: "correct-horse-battery", Role: role.String(),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.UserID == "" || members.saved == nil || members.saved.Role() != role {
				t.Errorf("membership not saved with %q: %+v", role, members.saved)
			}
		})
	}

	t.Run("tenant roles are rejected (they go through the tenant API)", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		_, err := newUC(tenant, newFakeMembershipRepo()).Execute(context.Background(), RegisterUserCmd{
			TenantID: tenant.ID().String(), Email: "x@y.com", Password: "correct-horse-battery", Role: "admin",
		})
		if !errors.Is(err, domain.ErrNotPlatformRole) {
			t.Fatalf("expected ErrNotPlatformRole, got %v", err)
		}
	})
}

func TestRegisterUser_RejectsPlatformRoles(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	uc := NewRegisterUserUseCase(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(),
		&fakeHasher{}, fakeTx{}, &fakePublisher{})
	for _, role := range []string{"platform_support", "compliance_officer"} {
		_, err := uc.Execute(context.Background(), RegisterUserCmd{
			TenantID: tenant.ID().String(), Email: "x@y.com", Password: "correct-horse-battery", Role: role,
		})
		if !errors.Is(err, domain.ErrRoleNotAssignable) {
			t.Errorf("%s: expected ErrRoleNotAssignable, got %v", role, err)
		}
	}
}
