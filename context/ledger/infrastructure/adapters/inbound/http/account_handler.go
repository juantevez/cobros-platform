package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type AccountHandler struct {
	createAccount *application.CreateAccountUseCase
	getBalance    *application.GetBalanceUseCase
}

func NewAccountHandler(
	createAccount *application.CreateAccountUseCase,
	getBalance *application.GetBalanceUseCase,
) *AccountHandler {
	return &AccountHandler{createAccount: createAccount, getBalance: getBalance}
}

// ── Create ────────────────────────────────────────────────────────────────────

type createAccountRequest struct {
	AccountType string `json:"account_type" binding:"required"`
	Currency    string `json:"currency"     binding:"required"`
	Description string `json:"description"`
}

type createAccountResponse struct {
	AccountID string `json:"account_id"`
}

// Create crea una cuenta contable para el tenant autenticado.
//
//	POST /api/v1/ledger/accounts
func (h *AccountHandler) Create(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.createAccount.Execute(c.Request.Context(), application.CreateAccountCmd{
		TenantID:    tenantID,
		AccountType: req.AccountType,
		Currency:    req.Currency,
		Description: req.Description,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusCreated, createAccountResponse{AccountID: result.AccountID})
}

// ── Balance ───────────────────────────────────────────────────────────────────

type getBalanceResponse struct {
	AccountID string `json:"account_id"`
	Balance   int64  `json:"balance"`
	Currency  string `json:"currency"`
}

// GetBalance retorna el saldo de una cuenta.
//
//	GET /api/v1/ledger/accounts/:accountID/balance
func (h *AccountHandler) GetBalance(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	result, err := h.getBalance.Execute(c.Request.Context(), application.GetBalanceQuery{
		TenantID:  tenantID,
		AccountID: c.Param("accountID"),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusOK, getBalanceResponse{
		AccountID: result.AccountID,
		Balance:   result.Balance,
		Currency:  result.Currency,
	})
}
