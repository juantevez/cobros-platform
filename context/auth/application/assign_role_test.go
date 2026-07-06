package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestAssignRole_CreatesMembershipWhenAbsent(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")

	tenantRepo := newFakeTenantRepo(tenant)
	userRepo := newFakeUserRepo(user, admin)
	memberRepo := newFakeMembershipRepo() // vacío → se crea
	pub := &fakePublisher{}
	uc := NewAssignRoleUseCase(tenantRepo, userRepo, memberRepo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID:   tenant.ID().String(),
		UserID:     user.ID().String(),
		Role:       "operator",
		AssignedBy: admin.ID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if memberRepo.saved == nil {
		t.Fatal("expected membership to be created")
	}
	if memberRepo.saved.Role() != domain.RoleOperator {
		t.Errorf("role = %q, want operator", memberRepo.saved.Role())
	}
	if memberRepo.updated != nil {
		t.Error("update should not be called on create path")
	}
	assertRoleAssignedEvent(t, pub, user.ID().String(), "operator")
}

func TestAssignRole_UpdatesExistingMembership(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	existing := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleReadOnly, admin.ID())

	tenantRepo := newFakeTenantRepo(tenant)
	userRepo := newFakeUserRepo(user, admin)
	memberRepo := newFakeMembershipRepo(existing)
	pub := &fakePublisher{}
	uc := NewAssignRoleUseCase(tenantRepo, userRepo, memberRepo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID:   tenant.ID().String(),
		UserID:     user.ID().String(),
		Role:       "admin",
		AssignedBy: admin.ID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if memberRepo.updated == nil {
		t.Fatal("expected membership to be updated")
	}
	if memberRepo.updated.Role() != domain.RoleAdmin {
		t.Errorf("role = %q, want admin", memberRepo.updated.Role())
	}
	if memberRepo.saved != nil {
		t.Error("save should not be called on update path")
	}
	assertRoleAssignedEvent(t, pub, user.ID().String(), "admin")
}

func TestAssignRole_TenantNotActive(t *testing.T) {
	tenant := newPendingTenant(t)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	uc := NewAssignRoleUseCase(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(), fakeTx{}, &fakePublisher{},
	)

	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID: tenant.ID().String(), UserID: user.ID().String(),
		Role: "operator", AssignedBy: domain.NewUserID().String(),
	})
	if !errors.Is(err, domain.ErrTenantNotActive) {
		t.Fatalf("expected ErrTenantNotActive, got %v", err)
	}
}

func TestAssignRole_UserFromAnotherTenant(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	otherTenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, otherTenant.ID(), "user@example.com") // pertenece a otro tenant

	uc := NewAssignRoleUseCase(
		newFakeTenantRepo(tenant, otherTenant), newFakeUserRepo(user),
		newFakeMembershipRepo(), fakeTx{}, &fakePublisher{},
	)

	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID: tenant.ID().String(), UserID: user.ID().String(),
		Role: "operator", AssignedBy: domain.NewUserID().String(),
	})
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestAssignRole_InvalidRole(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	uc := NewAssignRoleUseCase(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(), fakeTx{}, &fakePublisher{},
	)

	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID: tenant.ID().String(), UserID: user.ID().String(),
		Role: "wizard", AssignedBy: domain.NewUserID().String(),
	})
	if !errors.Is(err, domain.ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func assertRoleAssignedEvent(t *testing.T, pub *fakePublisher, wantUserID, wantRole string) {
	t.Helper()
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	ev, ok := pub.published[0].(domain.RoleAssignedEvent)
	if !ok {
		t.Fatalf("expected RoleAssignedEvent, got %T", pub.published[0])
	}
	if ev.UserID != wantUserID || ev.Role != wantRole {
		t.Errorf("event = %+v, want user=%s role=%s", ev, wantUserID, wantRole)
	}
}
