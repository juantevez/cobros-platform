package http

import (
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestLogin_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())

	env := newTestEnv(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(membership), newFakeApiKeyRepo(),
		&fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "ACCESS", refresh: "REFRESH"},
	)

	rec := env.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"tenant_id": tenant.ID().String(), "email": "user@example.com", "password": "secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body tokenResponse
	decodeBody(t, rec, &body)
	if body.AccessToken != "ACCESS" || body.RefreshToken != "REFRESH" || body.TokenType != "Bearer" {
		t.Errorf("unexpected token response: %+v", body)
	}
}

func TestLogin_BadBody(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "x@y.co"}) // falta tenant_id/password
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	env := newTestEnv(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(), newFakeApiKeyRepo(),
		&fakeHasher{verifyResult: false}, &fakeTokenIssuer{}, // password no coincide
	)
	rec := env.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"tenant_id": tenant.ID().String(), "email": "user@example.com", "password": "wrong",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLogin_SuspendedTenantForbidden(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	_ = tenant.Suspend("fraud")
	tenant.PullEvents()
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	env := newTestEnv(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(), newFakeApiKeyRepo(),
		&fakeHasher{verifyResult: true}, &fakeTokenIssuer{},
	)
	rec := env.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"tenant_id": tenant.ID().String(), "email": "user@example.com", "password": "secret",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRefresh_RoundTrip(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())
	env := newTestEnv(
		newFakeTenantRepo(tenant), newFakeUserRepo(user),
		newFakeMembershipRepo(membership), newFakeApiKeyRepo(),
		&fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "A", refresh: "RAWREFRESH"},
	)
	// Login primero para que el fakeRefreshRepo tenga el token guardado.
	login := env.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"tenant_id": tenant.ID().String(), "email": "user@example.com", "password": "secret",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	var lb tokenResponse
	decodeBody(t, login, &lb)

	// Ahora renovar con ese refresh token.
	rec := env.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": lb.RefreshToken,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefresh_InvalidToken(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": "ghost"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLogout_NoContent(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": "whatever"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
