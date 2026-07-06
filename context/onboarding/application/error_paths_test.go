package application

import (
	"context"
	"errors"
	"testing"
)

// error_paths_test.go ejercita la propagación de errores de infraestructura
// (Update / Publish dentro de la transacción) en los casos de uso.

func TestUploadDocument_UpdateErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	repo.updateErr = errBoom
	uc := NewUploadDocumentUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: tenantID.String(), DocumentType: "id_card", Reference: "x"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestAddPerson_UpdateErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	repo.updateErr = errBoom
	uc := NewAddPersonUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), AddPersonCmd{TenantID: tenantID.String(), FullName: "Juan", Role: "owner"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestSetBankAccount_UpdateErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	repo.updateErr = errBoom
	uc := NewSetBankAccountUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: tenantID.String(), AccountType: "CBU", AccountNumber: "1", HolderName: "A"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestSubmitForReview_UpdateErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(completePendingApp(t, tenantID))
	repo.updateErr = errBoom
	uc := NewSubmitForReviewUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), SubmitForReviewCmd{TenantID: tenantID.String()})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestSubmitForReview_PublisherErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(completePendingApp(t, tenantID))
	pub := &fakePublisher{err: errBoom}
	uc := NewSubmitForReviewUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), SubmitForReviewCmd{TenantID: tenantID.String()})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestReviewApplication_UpdateErrorPropagates(t *testing.T) {
	tenantID := testTenantID(t)
	app := inReviewApp(t, tenantID)
	repo := newFakeRepo(app)
	repo.updateErr = errBoom
	uc := NewReviewApplicationUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "approve", Notes: "ok"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
