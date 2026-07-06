package http

import (
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestIssueApiKey_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID(),
	})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/api-keys", "adm", map[string]any{
		"name": "WooCommerce", "environment": "production", "scopes": []string{"payments:read"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body issueApiKeyResponse
	decodeBody(t, rec, &body)
	if body.FullKey == "" || body.ApiKeyID == "" || body.Prefix == "" {
		t.Errorf("incomplete response: %+v", body)
	}
}

func TestIssueApiKey_TenantMismatchForbidden(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{
		Role: domain.RoleAdmin, TenantID: domain.NewTenantID(), UserID: domain.NewUserID(),
	})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/api-keys", "adm", map[string]any{
		"name": "k", "environment": "production", "scopes": []string{"payments:read"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestIssueApiKey_BadBody(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID()})

	// scopes vacío viola binding min=1.
	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/api-keys", "adm", map[string]any{
		"name": "k", "environment": "production", "scopes": []string{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRevokeApiKey_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	key := newApiKey(t, tenant.ID())
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin), newFakeMembershipRepo(), newFakeApiKeyRepo(key), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID()})

	rec := env.do(http.MethodDelete, "/api/v1/tenants/"+tenant.ID().String()+"/api-keys/"+key.ID().String(), "adm", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestRevokeApiKey_NotFound(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	admin := newActiveUser(t, tenant.ID(), "admin@example.com")
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(admin), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("adm", application.AccessTokenClaims{Role: domain.RoleAdmin, TenantID: tenant.ID(), UserID: admin.ID()})

	rec := env.do(http.MethodDelete, "/api/v1/tenants/"+tenant.ID().String()+"/api-keys/"+domain.NewApiKeyID().String(), "adm", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
