package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestParseIDs(t *testing.T) {
	if _, err := ParseApplicationID(uuid.NewString()); err != nil {
		t.Errorf("valid application id rejected: %v", err)
	}
	if _, err := ParseApplicationID("nope"); err == nil {
		t.Error("expected error for invalid application id")
	}
	if _, err := ParseTenantID(uuid.NewString()); err != nil {
		t.Errorf("valid tenant id rejected: %v", err)
	}
	if _, err := ParseTenantID("nope"); err == nil {
		t.Error("expected error for invalid tenant id")
	}
}

func TestApplicationStatus_IsEditable(t *testing.T) {
	editable := []ApplicationStatus{StatusPending, StatusRequiresMoreInfo}
	notEditable := []ApplicationStatus{StatusInReview, StatusApproved, StatusRejected}
	for _, s := range editable {
		if !s.IsEditable() {
			t.Errorf("%q should be editable", s)
		}
	}
	for _, s := range notEditable {
		if s.IsEditable() {
			t.Errorf("%q should not be editable", s)
		}
	}
}

func TestParseBusinessCategory(t *testing.T) {
	valid := []BusinessCategory{
		CategoryRetail, CategoryServices, CategoryFood, CategoryTechnology,
		CategoryHealthcare, CategoryEducation, CategoryMarketplace, CategoryOther,
	}
	for _, c := range valid {
		if got, err := ParseBusinessCategory(c.String()); err != nil || got != c {
			t.Errorf("ParseBusinessCategory(%q) = %v, %v", c, got, err)
		}
	}
	if _, err := ParseBusinessCategory("mining"); !errors.Is(err, ErrInvalidBusinessCat) {
		t.Errorf("expected ErrInvalidBusinessCat, got %v", err)
	}
}

func TestParseDocumentType(t *testing.T) {
	valid := []DocumentType{
		DocTypeIDCard, DocTypePassport, DocTypeBusinessReg, DocTypeTaxCertificate,
		DocTypeBankStatement, DocTypeProofOfAddress, DocTypeOwnershipProof,
	}
	for _, d := range valid {
		if got, err := ParseDocumentType(d.String()); err != nil || got != d {
			t.Errorf("ParseDocumentType(%q) = %v, %v", d, got, err)
		}
	}
	if _, err := ParseDocumentType("selfie"); !errors.Is(err, ErrInvalidDocumentType) {
		t.Errorf("expected ErrInvalidDocumentType, got %v", err)
	}
}

func TestParsePersonRole(t *testing.T) {
	for _, r := range []PersonRole{RoleOwner, RoleDirector, RoleUBO} {
		if got, err := ParsePersonRole(r.String()); err != nil || got != r {
			t.Errorf("ParsePersonRole(%q) = %v, %v", r, got, err)
		}
	}
	if _, err := ParsePersonRole("employee"); !errors.Is(err, ErrInvalidPersonRole) {
		t.Errorf("expected ErrInvalidPersonRole, got %v", err)
	}
}

func TestParseBankAccountType(t *testing.T) {
	// Se normaliza a mayúsculas.
	if got, err := ParseBankAccountType("cbu"); err != nil || got != BankAccountCBU {
		t.Errorf("ParseBankAccountType(cbu) = %v, %v", got, err)
	}
	if got, err := ParseBankAccountType("IBAN"); err != nil || got != BankAccountIBAN {
		t.Errorf("ParseBankAccountType(IBAN) = %v, %v", got, err)
	}
	if _, err := ParseBankAccountType("paypal"); !errors.Is(err, ErrInvalidAccountType) {
		t.Errorf("expected ErrInvalidAccountType, got %v", err)
	}
}

func TestParseTaxID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"strips dashes", "20-30405060", "2030405060", false},
		{"strips dots and slashes", "20.304.050/60", "2030405060", false},
		{"plain digits", "12345678", "12345678", false},
		{"too short", "1234567", "", true},
		{"too long", "123456789012345678901", "", true},
		{"non-digit", "20-3040AB-7", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTaxID(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidTaxID) {
					t.Fatalf("expected ErrInvalidTaxID, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAddress_IsComplete(t *testing.T) {
	full := Address{Street: "Calle 1", City: "CABA", Country: "AR"}
	if !full.IsComplete() {
		t.Error("full address should be complete")
	}
	if (Address{Street: "x", City: "y"}).IsComplete() {
		t.Error("address without country should be incomplete")
	}
	if (Address{City: "y", Country: "AR"}).IsComplete() {
		t.Error("address without street should be incomplete")
	}
}

func TestBusinessInfo_IsComplete(t *testing.T) {
	complete := BusinessInfo{
		LegalName:        "Acme SA",
		TaxID:            TaxID("20304050607"),
		BusinessCategory: CategoryRetail,
		Address:          Address{Street: "Calle 1", City: "CABA", Country: "AR"},
	}
	if !complete.IsComplete() {
		t.Error("complete business info should be complete")
	}

	missing := complete
	missing.TaxID = ""
	if missing.IsComplete() {
		t.Error("business info without tax id should be incomplete")
	}

	badAddr := complete
	badAddr.Address = Address{Street: "x"}
	if badAddr.IsComplete() {
		t.Error("business info with incomplete address should be incomplete")
	}
}
