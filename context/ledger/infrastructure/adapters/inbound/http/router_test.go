package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestRouter_RegistersAllRoutes verifica que las 4 rutas del ledger existen
// (no devuelven 404 de "ruta no encontrada").
func TestRouter_RegistersAllRoutes(t *testing.T) {
	env := newTestEnv(t)
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/ledger/accounts"},
		{http.MethodGet, "/api/v1/ledger/accounts/" + uuid.NewString() + "/balance"},
		{http.MethodPost, "/api/v1/ledger/entries"},
		{http.MethodPost, "/api/v1/ledger/entries/" + uuid.NewString() + "/reverse"},
	}
	for _, r := range routes {
		rec := env.do(r.method, r.path, map[string]any{})
		if rec.Code == http.StatusNotFound && rec.Body.Len() == 0 {
			t.Errorf("route %s %s not registered (bare 404)", r.method, r.path)
		}
	}
}

func TestRouter_UnknownRoute404(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/ledger/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
