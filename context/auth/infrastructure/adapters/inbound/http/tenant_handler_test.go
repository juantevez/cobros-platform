package http

import (
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRegisterTenant_Success(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodPost, "/api/v1/tenants", "", map[string]string{"legal_name": "Acme SA"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body registerTenantResponse
	decodeBody(t, rec, &body)
	if body.TenantID == "" {
		t.Error("expected a tenant id in response")
	}
}

func TestRegisterTenant_BadBody(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodPost, "/api/v1/tenants", "", map[string]string{}) // sin legal_name
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestActivateTenant_RequiresPlatformSupport(t *testing.T) {
	pending := newPendingTenant(t)
	env := newTestEnv(newFakeTenantRepo(pending), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})

	path := "/api/v1/tenants/" + pending.ID().String() + "/activate"
	body := map[string]string{"environment": "production"}

	t.Run("no token → 401", func(t *testing.T) {
		rec := env.do(http.MethodPost, path, "", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("operator role → 403", func(t *testing.T) {
		env.registerToken("op", application.AccessTokenClaims{Role: domain.RoleOperator, TenantID: domain.NewTenantID()})
		rec := env.do(http.MethodPost, path, "op", body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("platform_support → 204", func(t *testing.T) {
		env.registerToken("ps", application.AccessTokenClaims{Role: domain.RolePlatformSupport, TenantID: domain.NewTenantID()})
		rec := env.do(http.MethodPost, path, "ps", body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestSuspendTenant_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	env := newTestEnv(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("ps", application.AccessTokenClaims{Role: domain.RolePlatformSupport, TenantID: domain.NewTenantID()})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+tenant.ID().String()+"/suspend", "ps",
		map[string]string{"reason": "compliance issue"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSuspendTenant_NotFound(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	env.registerToken("ps", application.AccessTokenClaims{Role: domain.RolePlatformSupport, TenantID: domain.NewTenantID()})

	rec := env.do(http.MethodPost, "/api/v1/tenants/"+domain.NewTenantID().String()+"/suspend", "ps",
		map[string]string{"reason": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
