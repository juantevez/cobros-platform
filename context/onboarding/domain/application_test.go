package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testTenantID() TenantID { return TenantID(uuid.NewString()) }

func completeBusinessInfo() BusinessInfo {
	return BusinessInfo{
		LegalName:        "Acme SA",
		TaxID:            TaxID("20304050607"),
		BusinessCategory: CategoryRetail,
		Address:          Address{Street: "Calle 1", City: "CABA", Country: "AR"},
	}
}

// completeApp devuelve una aplicación pending lista para enviarse a revisión.
func completeApp(t *testing.T) *OnboardingApplication {
	t.Helper()
	a, err := NewOnboardingApplication(NewApplicationID(), testTenantID(), completeBusinessInfo())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.AddDocument(NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "s3://ref")); err != nil {
		t.Fatalf("add doc: %v", err)
	}
	if err := a.AddPerson(NewPerson(NewPersonID(), "Owner", RoleOwner, "DNI", "123", "AR")); err != nil {
		t.Fatalf("add person: %v", err)
	}
	if err := a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCBU, "001", "Banco", "Acme", "ARS")); err != nil {
		t.Fatalf("set bank: %v", err)
	}
	a.PullEvents() // limpiar el ApplicationSubmittedEvent inicial
	return a
}

func TestNewOnboardingApplication(t *testing.T) {
	t.Run("requires legal name", func(t *testing.T) {
		info := completeBusinessInfo()
		info.LegalName = ""
		if _, err := NewOnboardingApplication(NewApplicationID(), testTenantID(), info); err == nil {
			t.Fatal("expected error for missing legal name")
		}
	})

	t.Run("starts pending and emits ApplicationSubmitted", func(t *testing.T) {
		id := NewApplicationID()
		tid := testTenantID()
		a, err := NewOnboardingApplication(id, tid, completeBusinessInfo())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusPending {
			t.Errorf("status = %q, want pending", a.Status())
		}
		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		submitted, ok := events[0].(ApplicationSubmittedEvent)
		if !ok {
			t.Fatalf("expected ApplicationSubmittedEvent, got %T", events[0])
		}
		if submitted.ApplicationID != id.String() || submitted.LegalName != "Acme SA" {
			t.Errorf("event payload mismatch: %+v", submitted)
		}
	})
}

func TestSubmitForReview_Success(t *testing.T) {
	a := completeApp(t)
	if err := a.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if a.Status() != StatusInReview {
		t.Errorf("status = %q, want in_review", a.Status())
	}
	if a.SubmittedAt() == nil {
		t.Error("submittedAt should be set")
	}
	events := a.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if _, ok := events[0].(ApplicationSentForReviewEvent); !ok {
		t.Fatalf("expected ApplicationSentForReviewEvent, got %T", events[0])
	}
}

func TestSubmitForReview_Incomplete(t *testing.T) {
	tid := testTenantID()

	t.Run("missing documents", func(t *testing.T) {
		a, _ := NewOnboardingApplication(NewApplicationID(), tid, completeBusinessInfo())
		a.PullEvents()
		_ = a.AddPerson(NewPerson(NewPersonID(), "O", RoleOwner, "DNI", "1", "AR"))
		_ = a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCBU, "1", "B", "A", "ARS"))
		if err := a.SubmitForReview(); !errors.Is(err, ErrIncompleteApplication) {
			t.Fatalf("expected ErrIncompleteApplication, got %v", err)
		}
	})

	t.Run("missing persons", func(t *testing.T) {
		a, _ := NewOnboardingApplication(NewApplicationID(), tid, completeBusinessInfo())
		a.PullEvents()
		_ = a.AddDocument(NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "r"))
		_ = a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCBU, "1", "B", "A", "ARS"))
		if err := a.SubmitForReview(); !errors.Is(err, ErrIncompleteApplication) {
			t.Fatalf("expected ErrIncompleteApplication, got %v", err)
		}
	})

	t.Run("missing bank account", func(t *testing.T) {
		a, _ := NewOnboardingApplication(NewApplicationID(), tid, completeBusinessInfo())
		a.PullEvents()
		_ = a.AddDocument(NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "r"))
		_ = a.AddPerson(NewPerson(NewPersonID(), "O", RoleOwner, "DNI", "1", "AR"))
		if err := a.SubmitForReview(); !errors.Is(err, ErrIncompleteApplication) {
			t.Fatalf("expected ErrIncompleteApplication, got %v", err)
		}
	})

	t.Run("incomplete business info", func(t *testing.T) {
		info := completeBusinessInfo()
		info.TaxID = ""
		a, _ := NewOnboardingApplication(NewApplicationID(), tid, info)
		a.PullEvents()
		_ = a.AddDocument(NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "r"))
		_ = a.AddPerson(NewPerson(NewPersonID(), "O", RoleOwner, "DNI", "1", "AR"))
		_ = a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCBU, "1", "B", "A", "ARS"))
		if err := a.SubmitForReview(); !errors.Is(err, ErrIncompleteApplication) {
			t.Fatalf("expected ErrIncompleteApplication, got %v", err)
		}
	})
}

func TestApprove(t *testing.T) {
	t.Run("from in_review uses bank currency", func(t *testing.T) {
		a := completeApp(t)
		_ = a.SubmitForReview()
		a.PullEvents()

		if err := a.Approve("todo ok"); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if a.Status() != StatusApproved {
			t.Errorf("status = %q, want approved", a.Status())
		}
		if a.ReviewedAt() == nil || a.ReviewNotes() != "todo ok" {
			t.Error("review metadata not set")
		}
		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		approved, ok := events[0].(ApplicationApprovedEvent)
		if !ok {
			t.Fatalf("expected ApplicationApprovedEvent, got %T", events[0])
		}
		if approved.Currency != "ARS" || approved.BusinessCategory != "retail" {
			t.Errorf("event payload mismatch: %+v", approved)
		}
	})

	t.Run("defaults currency to ARS when no bank account", func(t *testing.T) {
		// Reconstituir un in_review sin cuenta bancaria para alcanzar el default.
		a := ReconstituteOnboardingApplication(
			NewApplicationID(), testTenantID(), StatusInReview,
			completeBusinessInfo(), nil, nil, nil, "", "", nil, nil,
			time.Now().UTC(), time.Now().UTC(),
		)
		if err := a.Approve(""); err != nil {
			t.Fatalf("approve: %v", err)
		}
		approved := a.PullEvents()[0].(ApplicationApprovedEvent)
		if approved.Currency != "ARS" {
			t.Errorf("currency = %q, want ARS default", approved.Currency)
		}
	})

	t.Run("cannot approve from pending", func(t *testing.T) {
		a := completeApp(t)
		if err := a.Approve("x"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

func TestReject(t *testing.T) {
	t.Run("from in_review with reason", func(t *testing.T) {
		a := completeApp(t)
		_ = a.SubmitForReview()
		a.PullEvents()

		if err := a.Reject("documentación falsa"); err != nil {
			t.Fatalf("reject: %v", err)
		}
		if a.Status() != StatusRejected || a.RejectionReason() != "documentación falsa" {
			t.Errorf("reject state mismatch: %+v", a.Status())
		}
		events := a.PullEvents()
		if rejected, ok := events[0].(ApplicationRejectedEvent); !ok || rejected.RejectionReason != "documentación falsa" {
			t.Errorf("expected ApplicationRejectedEvent, got %+v", events[0])
		}
	})

	t.Run("requires a reason", func(t *testing.T) {
		a := completeApp(t)
		_ = a.SubmitForReview()
		if err := a.Reject(""); !errors.Is(err, ErrRejectionReasonEmpty) {
			t.Fatalf("expected ErrRejectionReasonEmpty, got %v", err)
		}
	})

	t.Run("cannot reject from pending", func(t *testing.T) {
		a := completeApp(t)
		if err := a.Reject("x"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

func TestRequestMoreInfo(t *testing.T) {
	t.Run("from in_review with notes, then re-submit", func(t *testing.T) {
		a := completeApp(t)
		_ = a.SubmitForReview()
		a.PullEvents()

		if err := a.RequestMoreInfo("falta comprobante de domicilio"); err != nil {
			t.Fatalf("request more info: %v", err)
		}
		if a.Status() != StatusRequiresMoreInfo {
			t.Errorf("status = %q, want requires_more_info", a.Status())
		}
		if _, ok := a.PullEvents()[0].(MoreInfoRequestedEvent); !ok {
			t.Error("expected MoreInfoRequestedEvent")
		}

		// requires_more_info es editable → puede re-enviarse.
		if !a.Status().IsEditable() {
			t.Error("requires_more_info should be editable")
		}
		if err := a.SubmitForReview(); err != nil {
			t.Fatalf("re-submit: %v", err)
		}
		if a.Status() != StatusInReview {
			t.Errorf("status after re-submit = %q, want in_review", a.Status())
		}
	})

	t.Run("requires notes", func(t *testing.T) {
		a := completeApp(t)
		_ = a.SubmitForReview()
		if err := a.RequestMoreInfo(""); !errors.Is(err, ErrReviewNotesEmpty) {
			t.Fatalf("expected ErrReviewNotesEmpty, got %v", err)
		}
	})

	t.Run("cannot request from pending", func(t *testing.T) {
		a := completeApp(t)
		if err := a.RequestMoreInfo("x"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

func TestMutations_NotEditableInReview(t *testing.T) {
	a := completeApp(t)
	_ = a.SubmitForReview() // ahora in_review (no editable)

	if err := a.AddDocument(NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "r")); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("AddDocument in_review: expected ErrInvalidTransition, got %v", err)
	}
	if err := a.AddPerson(NewPerson(NewPersonID(), "P", RoleOwner, "DNI", "1", "AR")); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("AddPerson in_review: expected ErrInvalidTransition, got %v", err)
	}
	if err := a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCBU, "1", "B", "A", "ARS")); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("SetBankAccount in_review: expected ErrInvalidTransition, got %v", err)
	}
}

func TestSubmitForReview_NotEditable(t *testing.T) {
	a := completeApp(t)
	if err := a.SubmitForReview(); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// Ya está in_review (no editable) → volver a enviar falla.
	if err := a.SubmitForReview(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestApplication_Getters(t *testing.T) {
	tid := testTenantID()
	a, _ := NewOnboardingApplication(NewApplicationID(), tid, completeBusinessInfo())
	a.PullEvents()

	if a.TenantID() != tid {
		t.Errorf("tenantID = %q, want %q", a.TenantID(), tid)
	}
	if a.BusinessInfo().LegalName != "Acme SA" {
		t.Error("business info not exposed")
	}
	if a.BankAccount() != nil {
		t.Error("new application should have no bank account")
	}
	if a.SubmittedAt() != nil || a.ReviewedAt() != nil {
		t.Error("timestamps should be nil before submission/review")
	}
	if a.CreatedAt().IsZero() || a.UpdatedAt().IsZero() {
		t.Error("createdAt/updatedAt should be set")
	}

	// Tras setear una cuenta, el getter la expone.
	_ = a.SetBankAccount(NewBankAccount(NewBankAccountID(), BankAccountCVU, "1", "B", "A", "ARS"))
	if a.BankAccount() == nil || a.BankAccount().AccountType() != BankAccountCVU {
		t.Error("bank account getter not working after set")
	}
}

func TestReconstituteOnboardingApplication(t *testing.T) {
	id := NewApplicationID()
	tid := testTenantID()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := ReconstituteOnboardingApplication(
		id, tid, StatusApproved, completeBusinessInfo(), nil,
		[]BusinessDocument{NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "r")},
		[]Person{NewPerson(NewPersonID(), "O", RoleOwner, "DNI", "1", "AR")},
		"notas", "", nil, nil, created, created,
	)
	if a.ID() != id || a.Status() != StatusApproved || a.ReviewNotes() != "notas" {
		t.Errorf("not restored: %+v", a)
	}
	if len(a.Documents()) != 1 || len(a.Persons()) != 1 {
		t.Error("collections not restored")
	}
	if len(a.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}
