package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// error_paths_test.go ejercita la propagación de errores de infraestructura
// (repos, hasher, publisher) en los casos de uso, usando inyección en los fakes.

var errBoom = errors.New("boom")

func TestActivateTenant_FindErrorPropagates(t *testing.T) {
	repo := newFakeTenantRepo()
	repo.findErr = errBoom
	uc := NewActivateTenantUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), ActivateTenantCmd{
		TenantID: domain.NewTenantID().String(), Environment: "test",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestActivateTenant_UpdateErrorPropagates(t *testing.T) {
	tenant := newPendingTenant(t)
	repo := newFakeTenantRepo(tenant)
	repo.updateErr = errBoom
	uc := NewActivateTenantUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), ActivateTenantCmd{
		TenantID: tenant.ID().String(), Environment: "test",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestAssignRole_MembershipLookupErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	memberRepo := newFakeMembershipRepo()
	memberRepo.findErr = errBoom // error distinto de ErrMembershipNotFound

	uc := NewAssignRoleUseCase(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		memberRepo, fakeTx{}, &fakePublisher{},
	)
	err := uc.Execute(context.Background(), AssignRoleCmd{
		TenantID: tenant.ID().String(), UserID: user.ID().String(),
		Role: "operator", AssignedBy: domain.NewUserID().String(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestAuthenticate_VerifyErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	hasher := &fakeHasher{verifyErr: errBoom}

	uc := newAuthUC(tenant, user, nil, hasher, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestAuthenticate_MissingMembershipPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	// Sin membership → FindByUserAndTenant devuelve ErrMembershipNotFound.
	uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "a", refresh: "r"}, &fakeRefreshRepo{})

	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, domain.ErrMembershipNotFound) {
		t.Fatalf("expected ErrMembershipNotFound, got %v", err)
	}
}

func TestAuthenticate_RefreshTokenSaveErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())
	refreshRepo := &fakeRefreshRepo{saveErr: errBoom}

	uc := newAuthUC(tenant, user, []domain.Membership{membership},
		&fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "a", refresh: "r"}, refreshRepo)
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestIssueApiKey_HashErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	hasher := &fakeHasher{hashErr: errBoom}
	uc := NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), newFakeApiKeyRepo(), hasher, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), IssueApiKeyCmd{
		TenantID: tenant.ID().String(), Name: "k", Environment: "production", Scopes: []string{"payments:read"},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestIssueApiKey_SaveErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	apiRepo := newFakeApiKeyRepo()
	apiRepo.saveErr = errBoom
	uc := NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), apiRepo, &fakeHasher{}, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), IssueApiKeyCmd{
		TenantID: tenant.ID().String(), Name: "k", Environment: "production", Scopes: []string{"payments:read"},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}
