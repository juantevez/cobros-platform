package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func TestRespondDomainError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"account not found", domain.ErrAccountNotFound, http.StatusNotFound},
		{"entry not found", domain.ErrEntryNotFound, http.StatusNotFound},
		{"not balanced", domain.ErrEntryNotBalanced, http.StatusUnprocessableEntity},
		{"currency mismatch", domain.ErrCurrencyMismatch, http.StatusUnprocessableEntity},
		{"not enough postings", domain.ErrNotEnoughPostings, http.StatusUnprocessableEntity},
		{"zero amount", domain.ErrZeroAmount, http.StatusUnprocessableEntity},
		{"invalid direction", domain.ErrInvalidDirection, http.StatusUnprocessableEntity},
		{"invalid account type", domain.ErrInvalidAccountType, http.StatusUnprocessableEntity},
		{"invalid currency", domain.ErrInvalidCurrency, http.StatusUnprocessableEntity},
		{"negative amount", domain.ErrNegativeAmount, http.StatusUnprocessableEntity},
		{"already exists", domain.ErrEntryAlreadyExists, http.StatusConflict},
		{"unknown internal", errors.New("db exploded"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestCtx()
			respondDomainError(c, tt.err)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body ErrorResponse
			decodeBody(t, rec, &body)
			if body.Error == "" {
				t.Error("error message should not be empty")
			}
		})
	}
}

func TestRespondDomainError_InternalHidesDetails(t *testing.T) {
	c, rec := newTestCtx()
	respondDomainError(c, errors.New("sensitive connection string"))
	var body ErrorResponse
	decodeBody(t, rec, &body)
	if body.Error != "internal server error" {
		t.Errorf("internal error should be masked, got %q", body.Error)
	}
}
