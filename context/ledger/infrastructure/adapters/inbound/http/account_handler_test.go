package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func TestCreateAccount_Success(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/ledger/accounts", map[string]string{
		"account_type": "merchant_balance", "currency": "ARS", "description": "saldo",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body createAccountResponse
	decodeBody(t, rec, &body)
	if body.AccountID == "" {
		t.Error("expected an account id")
	}
	if len(env.pub.published) != 1 {
		t.Errorf("expected 1 event published, got %d", len(env.pub.published))
	}
}

func TestCreateAccount_BadBody(t *testing.T) {
	env := newTestEnv(t)
	// falta currency (binding required)
	rec := env.do(http.MethodPost, "/api/v1/ledger/accounts", map[string]string{"account_type": "reserve"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateAccount_InvalidAccountType(t *testing.T) {
	env := newTestEnv(t)
	// account_type inválido → dominio devuelve ErrInvalidAccountType → 422
	rec := env.do(http.MethodPost, "/api/v1/ledger/accounts", map[string]string{
		"account_type": "chicken", "currency": "ARS",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestGetBalance_Success(t *testing.T) {
	env := newTestEnv(t)
	acc, _ := domain.NewAccount(domain.NewAccountID(), env.tenantID, domain.AccountTypeMerchantBalance, "ARS", "")
	acc.PullEvents()
	env.accounts.byID[acc.ID()] = acc
	env.balances.balance = 9700

	rec := env.do(http.MethodGet, "/api/v1/ledger/accounts/"+acc.ID().String()+"/balance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body getBalanceResponse
	decodeBody(t, rec, &body)
	if body.Balance != 9700 || body.Currency != "ARS" || body.AccountID != acc.ID().String() {
		t.Errorf("unexpected balance response: %+v", body)
	}
}

func TestGetBalance_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/ledger/accounts/"+uuid.NewString()+"/balance", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
