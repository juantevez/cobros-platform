package http

import (
	"net/http"
	"testing"
)

func TestRouter_Health(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodGet, "/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRouter_CorrelationHeaderAlwaysPresent(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodGet, "/health", "", nil)
	if rec.Header().Get("X-Correlation-ID") == "" {
		t.Error("expected correlation id header on every response")
	}
}

func TestRouter_ProtectedRequiresAuth(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	// Ruta protegida sin token → 401 (JWTMiddleware aborta antes del handler).
	rec := env.do(http.MethodPost, "/api/v1/tenants/"+"any"+"/users", "", map[string]string{"email": "a@b.co"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRouter_UnknownRoute404(t *testing.T) {
	env := newTestEnv(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), newFakeApiKeyRepo(), &fakeHasher{}, &fakeTokenIssuer{})
	rec := env.do(http.MethodGet, "/api/v1/nonexistent", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
