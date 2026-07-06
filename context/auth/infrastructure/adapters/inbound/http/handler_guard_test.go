package http

import (
	"net/http"
	"testing"
)

// Estas guardas defensivas se ejecutan si un handler corre sin claims en el
// contexto (no debería pasar tras JWTMiddleware, pero se verifica igual).
// El caso de uso nunca se invoca, por eso los handlers se construyen con nil.

func TestUserHandler_Register_NoClaims(t *testing.T) {
	h := NewUserHandler(nil, nil)
	c, rec := newTestCtx()
	c.Request = c.Request.Clone(c.Request.Context())
	h.Register(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUserHandler_AssignRole_NoClaims(t *testing.T) {
	h := NewUserHandler(nil, nil)
	c, rec := newTestCtx()
	h.AssignRole(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestApiKeyHandler_Issue_NoClaims(t *testing.T) {
	h := NewApiKeyHandler(nil, nil)
	c, rec := newTestCtx()
	h.Issue(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestApiKeyHandler_Revoke_NoClaims(t *testing.T) {
	h := NewApiKeyHandler(nil, nil)
	c, rec := newTestCtx()
	h.Revoke(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
