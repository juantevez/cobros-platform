package http

import (
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRegisterUser_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID(),
	})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/users", "adm", map[string]string{
		"email": "new@example.com", "password": "s3cret", "role": "operator",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body registerUserResponse
	decodeBody(t, rec, &body)
	if body.UserID == "" {
		t.Error("expected a user id")
	}
}

func TestRegisterUser_TenantMismatchForbidden(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	// El token es admin pero de OTRO tenant que el del path.
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: domain.NewTenantID(), UserID: domain.NewUserID(),
	})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/users", "adm", map[string]string{
		"email": "new@example.com", "password": "s3cret", "role": "operator",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAssignRole_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	target := newActiveUser(t, tenant.ID(), "target@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin, target), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID(),
	})

	path := "/api/v1/tenants/" + tenant.ID().String() + "/members/" + target.ID().String() + "/role"
	rec := env.do(http.MethodPut, path, "adm", map[string]string{"role": "accountant"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAssignRole_InvalidRole(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	target := newActiveUser(t, tenant.ID(), "target@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin, target), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID(),
	})

	path := "/api/v1/tenants/" + tenant.ID().String() + "/members/" + target.ID().String() + "/role"
	rec := env.do(http.MethodPut, path, "adm", map[string]string{"role": "wizard"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
