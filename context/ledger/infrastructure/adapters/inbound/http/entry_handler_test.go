package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestPostEntry_Success(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "pay-123",
		"description":     "pago",
		"lines":           balancedLines(),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body postEntryResponse
	decodeBody(t, rec, &body)
	if body.EntryID == "" || body.WasExisting {
		t.Errorf("unexpected response: %+v", body)
	}
}

func TestPostEntry_Idempotent200(t *testing.T) {
	env := newTestEnv(t)
	// Preload un asiento con la misma clave de idempotencia del tenant.
	existing := buildEntry(t, env.tenantID, "pay-123")
	env.entries.preload(existing)

	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "pay-123",
		"lines":           balancedLines(),
	})
	// Idempotente: 200 (no 201) y was_existing=true.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body postEntryResponse
	decodeBody(t, rec, &body)
	if !body.WasExisting || body.EntryID != existing.ID().String() {
		t.Errorf("expected existing entry, got %+v", body)
	}
}

func TestPostEntry_BadBody(t *testing.T) {
	env := newTestEnv(t)
	// Solo 1 línea viola binding min=2.
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "k",
		"lines": []map[string]any{
			{"account_id": uuid.NewString(), "direction": "debit", "amount": 100, "currency": "ARS"},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPostEntry_Unbalanced422(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "k",
		"lines": []map[string]any{
			{"account_id": uuid.NewString(), "direction": "debit", "amount": 100, "currency": "ARS"},
			{"account_id": uuid.NewString(), "direction": "credit", "amount": 50, "currency": "ARS"},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestReverseEntry_Success(t *testing.T) {
	env := newTestEnv(t)
	original := buildEntry(t, env.tenantID, "orig-key")
	env.entries.byID[original.ID()] = original

	rec := env.do(http.MethodPost, "/api/v1/ledger/entries/"+original.ID().String()+"/reverse", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body reverseEntryResponse
	decodeBody(t, rec, &body)
	if body.ReverseEntryID == "" {
		t.Error("expected a reverse entry id")
	}
}

func TestReverseEntry_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries/"+uuid.NewString()+"/reverse", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
