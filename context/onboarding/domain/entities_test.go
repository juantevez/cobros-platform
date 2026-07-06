package domain

import (
	"testing"
	"time"
)

func TestNewBusinessDocument(t *testing.T) {
	doc := NewBusinessDocument(NewDocumentID(), DocTypeIDCard, "s3://bucket/doc.pdf")
	if doc.DocumentType() != DocTypeIDCard || doc.Reference() != "s3://bucket/doc.pdf" {
		t.Errorf("fields mismatch: %+v", doc)
	}
	if doc.Status() != DocStatusPending {
		t.Errorf("new document status = %q, want pending", doc.Status())
	}
	if doc.UploadedAt().IsZero() {
		t.Error("uploadedAt should be set")
	}
	if doc.ID().String() == "" {
		t.Error("document id string should not be empty")
	}
}

func TestReconstituteDocument(t *testing.T) {
	id := NewDocumentID()
	up := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	doc := ReconstituteDocument(id, DocTypePassport, "ref", "revisado ok", DocStatusVerified, up)
	if doc.ID() != id || doc.Status() != DocStatusVerified || doc.Notes() != "revisado ok" {
		t.Errorf("not restored: %+v", doc)
	}
	if !doc.UploadedAt().Equal(up) {
		t.Error("uploadedAt not restored")
	}
}

func TestNewPerson(t *testing.T) {
	p := NewPerson(NewPersonID(), "Juan Pérez", RoleOwner, "DNI", "12345678", "AR")
	if p.FullName() != "Juan Pérez" || p.Role() != RoleOwner {
		t.Errorf("fields mismatch: %+v", p)
	}
	if p.IdentityDocType() != "DNI" || p.IdentityDocNumber() != "12345678" || p.Nationality() != "AR" {
		t.Errorf("identity fields mismatch: %+v", p)
	}
	if p.CreatedAt().IsZero() {
		t.Error("createdAt should be set")
	}
	if p.ID().String() == "" {
		t.Error("person id string should not be empty")
	}
}

func TestReconstitutePerson(t *testing.T) {
	id := NewPersonID()
	created := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	p := ReconstitutePerson(id, "Ana", RoleUBO, "PASSPORT", "AA1", "US", created)
	if p.ID() != id || p.Role() != RoleUBO || p.FullName() != "Ana" {
		t.Errorf("not restored: %+v", p)
	}
	if !p.CreatedAt().Equal(created) {
		t.Error("createdAt not restored")
	}
}

func TestNewBankAccount(t *testing.T) {
	b := NewBankAccount(NewBankAccountID(), BankAccountCBU, "0011223344", "Banco Nación", "Acme SA", "ARS")
	if b.AccountType() != BankAccountCBU || b.AccountNumber() != "0011223344" {
		t.Errorf("fields mismatch: %+v", b)
	}
	if b.BankName() != "Banco Nación" || b.HolderName() != "Acme SA" || b.Currency() != "ARS" {
		t.Errorf("fields mismatch: %+v", b)
	}
	if b.Verified() {
		t.Error("new bank account should not be verified")
	}
	if b.ID().String() == "" || b.AccountType().String() != "CBU" {
		t.Errorf("id/type string mismatch: %q %q", b.ID(), b.AccountType())
	}
}

func TestReconstituteBankAccount(t *testing.T) {
	id := NewBankAccountID()
	created := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	b := ReconstituteBankAccount(id, BankAccountIBAN, "ES00", "BBVA", "Acme", "EUR", true, created)
	if b.ID() != id || b.AccountType() != BankAccountIBAN || !b.Verified() {
		t.Errorf("not restored: %+v", b)
	}
	if b.Currency() != "EUR" || !b.CreatedAt().Equal(created) {
		t.Error("currency/createdAt not restored")
	}
}
