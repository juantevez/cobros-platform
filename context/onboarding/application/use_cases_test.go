package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

func validSubmitCmd(tenantID string) SubmitApplicationCmd {
	return SubmitApplicationCmd{
		TenantID:         tenantID,
		LegalName:        "Acme SA",
		TaxID:            "20-30405060-7",
		BusinessCategory: "retail",
		Street:           "Calle 1",
		City:             "CABA",
		Country:          "AR",
	}
}

// ── SubmitApplication ─────────────────────────────────────────────────────────

func TestSubmitApplication_Success(t *testing.T) {
	repo := newFakeRepo()
	pub := &fakePublisher{}
	uc := NewSubmitApplicationUseCase(repo, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), validSubmitCmd(validUUID()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ApplicationID == "" {
		t.Fatal("expected an application id")
	}
	if repo.saved == nil {
		t.Fatal("application not saved")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.ApplicationSubmittedEvent); !ok {
		t.Fatalf("expected ApplicationSubmittedEvent, got %T", pub.published[0])
	}
}

func TestSubmitApplication_ValidationErrors(t *testing.T) {
	uc := NewSubmitApplicationUseCase(newFakeRepo(), fakeTx{}, &fakePublisher{})

	t.Run("invalid tenant id", func(t *testing.T) {
		cmd := validSubmitCmd("nope")
		if _, err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid tax id", func(t *testing.T) {
		cmd := validSubmitCmd(validUUID())
		cmd.TaxID = "abc"
		if _, err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidTaxID) {
			t.Fatalf("expected ErrInvalidTaxID, got %v", err)
		}
	})
	t.Run("invalid business category", func(t *testing.T) {
		cmd := validSubmitCmd(validUUID())
		cmd.BusinessCategory = "mining"
		if _, err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidBusinessCat) {
			t.Fatalf("expected ErrInvalidBusinessCat, got %v", err)
		}
	})
	t.Run("empty legal name", func(t *testing.T) {
		cmd := validSubmitCmd(validUUID())
		cmd.LegalName = ""
		if _, err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for empty legal name")
		}
	})
}

func TestSubmitApplication_AlreadyExists(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	uc := NewSubmitApplicationUseCase(repo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), validSubmitCmd(tenantID.String()))
	if !errors.Is(err, domain.ErrApplicationExists) {
		t.Fatalf("expected ErrApplicationExists, got %v", err)
	}
}

func TestSubmitApplication_SaveErrorPropagates(t *testing.T) {
	repo := newFakeRepo()
	repo.saveErr = errBoom
	uc := NewSubmitApplicationUseCase(repo, fakeTx{}, &fakePublisher{})

	if _, err := uc.Execute(context.Background(), validSubmitCmd(validUUID())); !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

// ── UploadDocument ────────────────────────────────────────────────────────────

func TestUploadDocument_Success(t *testing.T) {
	tenantID := testTenantID(t)
	app := pendingApp(t, tenantID)
	repo := newFakeRepo(app)
	uc := NewUploadDocumentUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), UploadDocumentCmd{
		TenantID: tenantID.String(), DocumentType: "id_card", Reference: "s3://x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || len(repo.updated.Documents()) != 1 {
		t.Fatal("document not added / not updated")
	}
}

func TestUploadDocument_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("invalid document type", func(t *testing.T) {
		uc := NewUploadDocumentUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: tenantID.String(), DocumentType: "selfie", Reference: "x"})
		if !errors.Is(err, domain.ErrInvalidDocumentType) {
			t.Fatalf("expected ErrInvalidDocumentType, got %v", err)
		}
	})
	t.Run("empty reference", func(t *testing.T) {
		uc := NewUploadDocumentUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: tenantID.String(), DocumentType: "id_card", Reference: ""})
		if err == nil {
			t.Fatal("expected error for empty reference")
		}
	})
	t.Run("invalid tenant id", func(t *testing.T) {
		uc := NewUploadDocumentUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: "nope", DocumentType: "id_card", Reference: "x"})
		if err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})
	t.Run("application not found", func(t *testing.T) {
		uc := NewUploadDocumentUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: validUUID(), DocumentType: "id_card", Reference: "x"})
		if err == nil {
			t.Fatal("expected error when application not found")
		}
	})
	t.Run("not editable in review", func(t *testing.T) {
		uc := NewUploadDocumentUseCase(newFakeRepo(inReviewApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), UploadDocumentCmd{TenantID: tenantID.String(), DocumentType: "id_card", Reference: "x"})
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

// ── AddPerson ─────────────────────────────────────────────────────────────────

func TestAddPerson_Success(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	uc := NewAddPersonUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), AddPersonCmd{
		TenantID: tenantID.String(), FullName: "Juan", Role: "owner",
		IdentityDocType: "DNI", IdentityDocNumber: "123", Nationality: "AR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || len(repo.updated.Persons()) != 1 {
		t.Fatal("person not added / not updated")
	}
}

func TestAddPerson_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("empty full name", func(t *testing.T) {
		uc := NewAddPersonUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), AddPersonCmd{TenantID: tenantID.String(), FullName: "", Role: "owner"})
		if err == nil {
			t.Fatal("expected error for empty full name")
		}
	})
	t.Run("invalid role", func(t *testing.T) {
		uc := NewAddPersonUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), AddPersonCmd{TenantID: tenantID.String(), FullName: "Juan", Role: "employee"})
		if !errors.Is(err, domain.ErrInvalidPersonRole) {
			t.Fatalf("expected ErrInvalidPersonRole, got %v", err)
		}
	})
	t.Run("invalid tenant id", func(t *testing.T) {
		uc := NewAddPersonUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), AddPersonCmd{TenantID: "nope", FullName: "Juan", Role: "owner"})
		if err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})
	t.Run("application not found", func(t *testing.T) {
		uc := NewAddPersonUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), AddPersonCmd{TenantID: validUUID(), FullName: "Juan", Role: "owner"})
		if err == nil {
			t.Fatal("expected error when application not found")
		}
	})
	t.Run("not editable in review", func(t *testing.T) {
		uc := NewAddPersonUseCase(newFakeRepo(inReviewApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), AddPersonCmd{TenantID: tenantID.String(), FullName: "Juan", Role: "owner"})
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

// ── SetBankAccount ────────────────────────────────────────────────────────────

func TestSetBankAccount_Success(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(pendingApp(t, tenantID))
	uc := NewSetBankAccountUseCase(repo, fakeTx{})

	err := uc.Execute(context.Background(), SetBankAccountCmd{
		TenantID: tenantID.String(), AccountType: "cbu", AccountNumber: "001",
		HolderName: "Acme", Currency: "ARS",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || repo.updated.BankAccount() == nil {
		t.Fatal("bank account not set / not updated")
	}
}

func TestSetBankAccount_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("invalid account type", func(t *testing.T) {
		uc := NewSetBankAccountUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: tenantID.String(), AccountType: "paypal", AccountNumber: "1", HolderName: "A"})
		if !errors.Is(err, domain.ErrInvalidAccountType) {
			t.Fatalf("expected ErrInvalidAccountType, got %v", err)
		}
	})
	t.Run("missing account number or holder", func(t *testing.T) {
		uc := NewSetBankAccountUseCase(newFakeRepo(pendingApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: tenantID.String(), AccountType: "CBU", AccountNumber: "", HolderName: ""})
		if err == nil {
			t.Fatal("expected error for missing account number / holder")
		}
	})
	t.Run("invalid tenant id", func(t *testing.T) {
		uc := NewSetBankAccountUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: "nope", AccountType: "CBU", AccountNumber: "1", HolderName: "A"})
		if err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})
	t.Run("application not found", func(t *testing.T) {
		uc := NewSetBankAccountUseCase(newFakeRepo(), fakeTx{})
		err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: validUUID(), AccountType: "CBU", AccountNumber: "1", HolderName: "A"})
		if err == nil {
			t.Fatal("expected error when application not found")
		}
	})
	t.Run("not editable in review", func(t *testing.T) {
		uc := NewSetBankAccountUseCase(newFakeRepo(inReviewApp(t, tenantID)), fakeTx{})
		err := uc.Execute(context.Background(), SetBankAccountCmd{TenantID: tenantID.String(), AccountType: "CBU", AccountNumber: "1", HolderName: "A"})
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

// ── SubmitForReview ───────────────────────────────────────────────────────────

func TestSubmitForReview_Success(t *testing.T) {
	tenantID := testTenantID(t)
	repo := newFakeRepo(completePendingApp(t, tenantID))
	pub := &fakePublisher{}
	uc := NewSubmitForReviewUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), SubmitForReviewCmd{TenantID: tenantID.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated == nil || repo.updated.Status() != domain.StatusInReview {
		t.Fatal("application not moved to in_review")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.ApplicationSentForReviewEvent); !ok {
		t.Fatalf("expected ApplicationSentForReviewEvent, got %T", pub.published[0])
	}
}

func TestSubmitForReview_Incomplete(t *testing.T) {
	tenantID := testTenantID(t)
	// pendingApp sin documentos/personas/cuenta → incompleta.
	repo := newFakeRepo(pendingApp(t, tenantID))
	uc := NewSubmitForReviewUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), SubmitForReviewCmd{TenantID: tenantID.String()})
	if !errors.Is(err, domain.ErrIncompleteApplication) {
		t.Fatalf("expected ErrIncompleteApplication, got %v", err)
	}
}

func TestSubmitForReview_NotFound(t *testing.T) {
	uc := NewSubmitForReviewUseCase(newFakeRepo(), fakeTx{}, &fakePublisher{})
	err := uc.Execute(context.Background(), SubmitForReviewCmd{TenantID: validUUID()})
	if err == nil {
		t.Fatal("expected error when application not found")
	}
}

// ── ReviewApplication ─────────────────────────────────────────────────────────

func TestReviewApplication_Approve(t *testing.T) {
	tenantID := testTenantID(t)
	app := inReviewApp(t, tenantID)
	repo := newFakeRepo(app)
	pub := &fakePublisher{}
	uc := NewReviewApplicationUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "approve", Notes: "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated.Status() != domain.StatusApproved {
		t.Errorf("status = %q, want approved", repo.updated.Status())
	}
	if _, ok := pub.published[0].(domain.ApplicationApprovedEvent); !ok {
		t.Fatalf("expected ApplicationApprovedEvent, got %T", pub.published[0])
	}
}

func TestReviewApplication_Reject(t *testing.T) {
	tenantID := testTenantID(t)
	app := inReviewApp(t, tenantID)
	repo := newFakeRepo(app)
	pub := &fakePublisher{}
	uc := NewReviewApplicationUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "reject", Notes: "docs falsos"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated.Status() != domain.StatusRejected {
		t.Errorf("status = %q, want rejected", repo.updated.Status())
	}
	if _, ok := pub.published[0].(domain.ApplicationRejectedEvent); !ok {
		t.Fatalf("expected ApplicationRejectedEvent, got %T", pub.published[0])
	}
}

func TestReviewApplication_RequestMoreInfo(t *testing.T) {
	tenantID := testTenantID(t)
	app := inReviewApp(t, tenantID)
	repo := newFakeRepo(app)
	uc := NewReviewApplicationUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "request_more_info", Notes: "falta comprobante"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated.Status() != domain.StatusRequiresMoreInfo {
		t.Errorf("status = %q, want requires_more_info", repo.updated.Status())
	}
}

func TestReviewApplication_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("reject without notes", func(t *testing.T) {
		app := inReviewApp(t, tenantID)
		uc := NewReviewApplicationUseCase(newFakeRepo(app), fakeTx{}, &fakePublisher{})
		err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "reject", Notes: ""})
		if !errors.Is(err, domain.ErrRejectionReasonEmpty) {
			t.Fatalf("expected ErrRejectionReasonEmpty, got %v", err)
		}
	})
	t.Run("invalid decision", func(t *testing.T) {
		app := inReviewApp(t, tenantID)
		uc := NewReviewApplicationUseCase(newFakeRepo(app), fakeTx{}, &fakePublisher{})
		err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "maybe", Notes: "x"})
		if err == nil {
			t.Fatal("expected error for invalid decision")
		}
	})
	t.Run("invalid application id", func(t *testing.T) {
		uc := NewReviewApplicationUseCase(newFakeRepo(), fakeTx{}, &fakePublisher{})
		err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: "nope", Decision: "approve"})
		if err == nil {
			t.Fatal("expected error for invalid application id")
		}
	})
	t.Run("not found", func(t *testing.T) {
		uc := NewReviewApplicationUseCase(newFakeRepo(), fakeTx{}, &fakePublisher{})
		err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: validUUID(), Decision: "approve"})
		if err == nil {
			t.Fatal("expected error when application not found")
		}
	})
	t.Run("approve from pending is invalid transition", func(t *testing.T) {
		app := completePendingApp(t, tenantID) // aún pending
		uc := NewReviewApplicationUseCase(newFakeRepo(app), fakeTx{}, &fakePublisher{})
		err := uc.Execute(context.Background(), ReviewApplicationCmd{ApplicationID: app.ID().String(), Decision: "approve", Notes: "x"})
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

// ── GetApplication ────────────────────────────────────────────────────────────

func TestGetApplication_Success(t *testing.T) {
	tenantID := testTenantID(t)
	app := completePendingApp(t, tenantID)
	repo := newFakeRepo(app)
	uc := NewGetApplicationUseCase(repo)

	view, err := uc.Execute(context.Background(), GetApplicationQuery{TenantID: tenantID.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.ID != app.ID().String() || view.Status != "pending" {
		t.Errorf("view mismatch: %+v", view)
	}
	if view.DocumentCount != 1 || view.PersonCount != 1 || !view.HasBankAccount {
		t.Errorf("counts mismatch: docs=%d persons=%d bank=%v", view.DocumentCount, view.PersonCount, view.HasBankAccount)
	}
	if view.LegalName != "Acme SA" || view.BusinessCategory != "retail" {
		t.Errorf("business info mismatch: %+v", view)
	}
}

func TestGetApplication_Errors(t *testing.T) {
	t.Run("invalid tenant id", func(t *testing.T) {
		uc := NewGetApplicationUseCase(newFakeRepo())
		if _, err := uc.Execute(context.Background(), GetApplicationQuery{TenantID: "nope"}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("not found", func(t *testing.T) {
		uc := NewGetApplicationUseCase(newFakeRepo())
		if _, err := uc.Execute(context.Background(), GetApplicationQuery{TenantID: validUUID()}); !errors.Is(err, domain.ErrApplicationNotFound) {
			t.Fatalf("expected ErrApplicationNotFound, got %v", err)
		}
	})
}
