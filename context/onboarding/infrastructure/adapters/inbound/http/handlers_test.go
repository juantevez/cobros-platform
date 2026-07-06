package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

func validSubmitReq() map[string]string {
	return map[string]string{
		"legal_name": "Acme SA", "tax_id": "20-30405060-7", "business_category": "retail",
		"city": "CABA", "country": "AR",
	}
}

func TestSubmit_Success(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/onboarding", validSubmitReq())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["application_id"] == "" {
		t.Error("expected an application id")
	}
}

func TestSubmit_BadBody(t *testing.T) {
	env := newTestEnv(t)
	// falta country (binding required)
	rec := env.do(http.MethodPost, "/api/v1/onboarding", map[string]string{"legal_name": "X", "tax_id": "1", "business_category": "retail", "city": "CABA"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmit_AlreadyExists409(t *testing.T) {
	env := newTestEnv(t)
	// Preload una app para el tenant del env.
	env.repo.byTenant[env.tenantID] = pendingApp(t, env.tenantID)

	rec := env.do(http.MethodPost, "/api/v1/onboarding", validSubmitReq())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestSubmit_InvalidBusinessCategory400(t *testing.T) {
	env := newTestEnv(t)
	req := validSubmitReq()
	req["business_category"] = "mining"
	rec := env.do(http.MethodPost, "/api/v1/onboarding", req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGet_Success(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = completePendingApp(t, env.tenantID)

	rec := env.do(http.MethodGet, "/api/v1/onboarding", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	decodeBody(t, rec, &view)
	if view["status"] != "pending" || view["document_count"].(float64) != 1 {
		t.Errorf("unexpected view: %+v", view)
	}
}

func TestGet_NotFound404(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/onboarding", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUploadDocument_Success(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = pendingApp(t, env.tenantID)

	rec := env.do(http.MethodPost, "/api/v1/onboarding/documents", map[string]string{
		"document_type": "id_card", "reference": "s3://x",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadDocument_NotEditable422(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = inReviewApp(t, env.tenantID)

	rec := env.do(http.MethodPost, "/api/v1/onboarding/documents", map[string]string{
		"document_type": "id_card", "reference": "s3://x",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAddPerson_Success(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = pendingApp(t, env.tenantID)

	rec := env.do(http.MethodPost, "/api/v1/onboarding/persons", map[string]string{
		"full_name": "Juan", "role": "owner",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadDocument_BadBody400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/onboarding/documents", map[string]string{"document_type": "id_card"}) // falta reference
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAddPerson_BadBody400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/onboarding/persons", map[string]string{"full_name": "Juan"}) // falta role
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAddPerson_NotEditable422(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = inReviewApp(t, env.tenantID)
	rec := env.do(http.MethodPost, "/api/v1/onboarding/persons", map[string]string{"full_name": "Juan", "role": "owner"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestSetBankAccount_BadBody400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/v1/onboarding/bank-account", map[string]string{"account_type": "CBU"}) // faltan campos
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSetBankAccount_NotEditable422(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = inReviewApp(t, env.tenantID)
	rec := env.do(http.MethodPut, "/api/v1/onboarding/bank-account", map[string]string{
		"account_type": "CBU", "account_number": "1", "holder_name": "A", "currency": "ARS",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestSetBankAccount_Success204(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = pendingApp(t, env.tenantID)

	rec := env.do(http.MethodPut, "/api/v1/onboarding/bank-account", map[string]string{
		"account_type": "CBU", "account_number": "001", "holder_name": "Acme", "currency": "ARS",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmitForReview_Success204(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = completePendingApp(t, env.tenantID)

	rec := env.do(http.MethodPost, "/api/v1/onboarding/submit", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmitForReview_Incomplete422(t *testing.T) {
	env := newTestEnv(t)
	env.repo.byTenant[env.tenantID] = pendingApp(t, env.tenantID) // incompleta

	rec := env.do(http.MethodPost, "/api/v1/onboarding/submit", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// ── Review (operador) ─────────────────────────────────────────────────────────

func TestReview_Approve204(t *testing.T) {
	env := newTestEnv(t)
	app := inReviewApp(t, env.tenantID)
	env.repo.byID[app.ID()] = app

	rec := env.do(http.MethodPost, "/api/v1/onboarding/applications/"+app.ID().String()+"/review",
		map[string]string{"decision": "approve", "notes": "ok"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestReview_RejectWithoutNotes400(t *testing.T) {
	env := newTestEnv(t)
	app := inReviewApp(t, env.tenantID)
	env.repo.byID[app.ID()] = app

	rec := env.do(http.MethodPost, "/api/v1/onboarding/applications/"+app.ID().String()+"/review",
		map[string]string{"decision": "reject"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestReview_BadBody400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/onboarding/applications/"+uuid.NewString()+"/review", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ── respondDomainError ────────────────────────────────────────────────────────

func TestRespondDomainError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found", domain.ErrApplicationNotFound, http.StatusNotFound},
		{"exists", domain.ErrApplicationExists, http.StatusConflict},
		{"invalid transition", domain.ErrInvalidTransition, http.StatusUnprocessableEntity},
		{"incomplete", domain.ErrIncompleteApplication, http.StatusUnprocessableEntity},
		{"invalid tax id", domain.ErrInvalidTaxID, http.StatusBadRequest},
		{"invalid category", domain.ErrInvalidBusinessCat, http.StatusBadRequest},
		{"invalid doc type", domain.ErrInvalidDocumentType, http.StatusBadRequest},
		{"invalid person role", domain.ErrInvalidPersonRole, http.StatusBadRequest},
		{"invalid account type", domain.ErrInvalidAccountType, http.StatusBadRequest},
		{"rejection reason empty", domain.ErrRejectionReasonEmpty, http.StatusBadRequest},
		{"review notes empty", domain.ErrReviewNotesEmpty, http.StatusBadRequest},
		{"unknown internal", errors.New("db exploded"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestCtx()
			respondDomainError(c, tt.err)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestRouter_UnknownRoute404(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/onboarding/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
