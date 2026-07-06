package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRespondDomainError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"invalid email", domain.ErrInvalidEmail, http.StatusBadRequest},
		{"invalid role", domain.ErrInvalidRole, http.StatusBadRequest},
		{"invalid id", domain.ErrInvalidID, http.StatusBadRequest},
		{"empty legal name", domain.ErrEmptyLegalName, http.StatusBadRequest},
		{"empty password", domain.ErrEmptyPassword, http.StatusBadRequest},
		{"invalid credentials", domain.ErrInvalidCredentials, http.StatusUnauthorized},
		{"api key revoked", domain.ErrApiKeyRevoked, http.StatusUnauthorized},
		{"tenant suspended", domain.ErrTenantSuspended, http.StatusForbidden},
		{"user suspended", domain.ErrUserSuspended, http.StatusForbidden},
		{"tenant not active", domain.ErrTenantNotActive, http.StatusForbidden},
		{"tenant not found", domain.ErrTenantNotFound, http.StatusNotFound},
		{"user not found", domain.ErrUserNotFound, http.StatusNotFound},
		{"api key not found", domain.ErrApiKeyNotFound, http.StatusNotFound},
		{"membership not found", domain.ErrMembershipNotFound, http.StatusNotFound},
		{"email exists", domain.ErrEmailAlreadyExists, http.StatusConflict},
		{"api key already revoked", domain.ErrApiKeyAlreadyRevoked, http.StatusConflict},
		{"cannot transition", domain.ErrTenantCannotTransition, http.StatusUnprocessableEntity},
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
	respondDomainError(c, errors.New("sensitive db connection string leaked"))

	var body ErrorResponse
	decodeBody(t, rec, &body)
	if body.Error != "internal server error" {
		t.Errorf("internal error should be masked, got %q", body.Error)
	}
}
