package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func TestPostEntry_Success(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "pay-123",
		"description":     "pago",
		"lines":           env.balancedLines(),
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
	existing := env.entry(t, "pay-123", 100)
	env.entries.preload(existing)

	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "pay-123",
		"lines":           env.balancedLines(),
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

func TestPostEntry_IdempotencyKeyConflict409(t *testing.T) {
	env := newTestEnv(t)
	env.entries.preload(env.entry(t, "pay-123", 100))

	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "pay-123",
		"lines":           env.lines(500, 500),
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
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
		"lines":           env.lines(100, 50),
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

// ── Autorización y aislamiento ────────────────────────────────────────────────

// Sin autorización (platform_support en producción) no se puede postear: un
// comercio podría acreditar su propio merchant_balance y retirarlo vía payout.
func TestPostEntry_GuardRejects(t *testing.T) {
	env := newTestEnvWithGuard(t, denyWrites)
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{
		"idempotency_key": "k",
		"lines":           env.balancedLines(),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(env.entries.byID) != 0 || len(env.pub.published) != 0 {
		t.Error("entry must not be posted when the guard rejects")
	}
}

func TestReverseEntry_GuardRejects(t *testing.T) {
	env := newTestEnvWithGuard(t, denyWrites)
	original := buildEntry(t, env.tenantID, "orig-key")
	env.entries.byID[original.ID()] = original

	rec := env.do(http.MethodPost, "/api/v1/ledger/entries/"+original.ID().String()+"/reverse", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestPostEntry_AccountOfAnotherTenant404(t *testing.T) {
	env := newTestEnv(t)
	foreign := newTestAccount(t, domain.TenantID(uuid.NewString()), domain.AccountTypeMerchantBalance, "ARS")
	env.accounts.byID[foreign.ID()] = foreign

	lines := env.balancedLines()
	lines[1]["account_id"] = foreign.ID().String()
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{"idempotency_key": "k", "lines": lines})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPostEntry_CurrencyDifferentFromAccount422(t *testing.T) {
	env := newTestEnv(t)
	lines := env.balancedLines()
	for _, l := range lines {
		l["currency"] = "USD" // las cuentas del tenant son ARS
	}
	rec := env.do(http.MethodPost, "/api/v1/ledger/entries", map[string]any{"idempotency_key": "k", "lines": lines})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// Un ID que no es UUID es un error del cliente, no un 500.
func TestInvalidIDs400(t *testing.T) {
	env := newTestEnv(t)
	lines := env.balancedLines()
	lines[0]["account_id"] = "not-a-uuid"

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"post entry", http.MethodPost, "/api/v1/ledger/entries", map[string]any{"idempotency_key": "k", "lines": lines}},
		{"reverse", http.MethodPost, "/api/v1/ledger/entries/not-a-uuid/reverse", nil},
		{"balance", http.MethodGet, "/api/v1/ledger/accounts/not-a-uuid/balance", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(tc.method, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
