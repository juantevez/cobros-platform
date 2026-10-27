package domain

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type ApplicationID string
type DocumentID string
type PersonID string
type BankAccountID string
type TenantID string

func NewApplicationID() ApplicationID { return ApplicationID(uuid.NewString()) }
func NewDocumentID() DocumentID       { return DocumentID(uuid.NewString()) }
func NewPersonID() PersonID           { return PersonID(uuid.NewString()) }
func NewBankAccountID() BankAccountID { return BankAccountID(uuid.NewString()) }

func ParseApplicationID(s string) (ApplicationID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: application id %q", ErrInvalidID, s)
	}
	return ApplicationID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: tenant id %q", ErrInvalidID, s)
	}
	return TenantID(s), nil
}

func (id ApplicationID) String() string { return string(id) }
func (id DocumentID) String() string    { return string(id) }
func (id PersonID) String() string      { return string(id) }
func (id BankAccountID) String() string { return string(id) }
func (id TenantID) String() string      { return string(id) }

// ── ApplicationStatus ─────────────────────────────────────────────────────────

type ApplicationStatus string

const (
	// StatusPending: el comercio está completando la información. Puede editar.
	StatusPending ApplicationStatus = "pending"
	// StatusInReview: enviado para revisión. El operador debe aprobar/rechazar.
	StatusInReview ApplicationStatus = "in_review"
	// StatusApproved: aprobado. El Tenant queda habilitado para producción.
	StatusApproved ApplicationStatus = "approved"
	// StatusRejected: rechazado. Estado final.
	StatusRejected ApplicationStatus = "rejected"
	// StatusRequiresMoreInfo: el operador pidió más documentación.
	StatusRequiresMoreInfo ApplicationStatus = "requires_more_info"
)

func (s ApplicationStatus) String() string { return string(s) }

// IsEditable retorna true si el comercio puede modificar la aplicación.
func (s ApplicationStatus) IsEditable() bool {
	return s == StatusPending || s == StatusRequiresMoreInfo
}

// ── BusinessCategory ──────────────────────────────────────────────────────────

type BusinessCategory string

const (
	CategoryRetail      BusinessCategory = "retail"
	CategoryServices    BusinessCategory = "services"
	CategoryFood        BusinessCategory = "food_beverage"
	CategoryTechnology  BusinessCategory = "technology"
	CategoryHealthcare  BusinessCategory = "healthcare"
	CategoryEducation   BusinessCategory = "education"
	CategoryMarketplace BusinessCategory = "marketplace"
	CategoryOther       BusinessCategory = "other"
)

func ParseBusinessCategory(s string) (BusinessCategory, error) {
	c := BusinessCategory(s)
	switch c {
	case CategoryRetail, CategoryServices, CategoryFood, CategoryTechnology,
		CategoryHealthcare, CategoryEducation, CategoryMarketplace, CategoryOther:
		return c, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidBusinessCat, s)
}

func (c BusinessCategory) String() string { return string(c) }

// ── DocumentType ──────────────────────────────────────────────────────────────

type DocumentType string

const (
	DocTypeIDCard         DocumentType = "id_card"
	DocTypePassport       DocumentType = "passport"
	DocTypeBusinessReg    DocumentType = "business_registration" // acta constitutiva
	DocTypeTaxCertificate DocumentType = "tax_certificate"       // constancia AFIP/RUT
	DocTypeBankStatement  DocumentType = "bank_statement"
	DocTypeProofOfAddress DocumentType = "proof_of_address"
	DocTypeOwnershipProof DocumentType = "ownership_proof"
)

func ParseDocumentType(s string) (DocumentType, error) {
	d := DocumentType(s)
	switch d {
	case DocTypeIDCard, DocTypePassport, DocTypeBusinessReg,
		DocTypeTaxCertificate, DocTypeBankStatement,
		DocTypeProofOfAddress, DocTypeOwnershipProof:
		return d, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidDocumentType, s)
}

func (d DocumentType) String() string { return string(d) }

// ── PersonRole ────────────────────────────────────────────────────────────────

type PersonRole string

const (
	RoleOwner    PersonRole = "owner"    // dueño/socio
	RoleDirector PersonRole = "director" // director/apoderado
	RoleUBO      PersonRole = "ubo"      // beneficiario final (Ultimate Beneficial Owner)
)

func ParsePersonRole(s string) (PersonRole, error) {
	r := PersonRole(s)
	switch r {
	case RoleOwner, RoleDirector, RoleUBO:
		return r, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidPersonRole, s)
}

func (r PersonRole) String() string { return string(r) }

// ── BankAccountType ───────────────────────────────────────────────────────────

type BankAccountType string

const (
	BankAccountCBU  BankAccountType = "CBU"  // Argentina
	BankAccountCVU  BankAccountType = "CVU"  // Argentina (billeteras)
	BankAccountIBAN BankAccountType = "IBAN" // Internacional
)

func ParseBankAccountType(s string) (BankAccountType, error) {
	t := BankAccountType(strings.ToUpper(s))
	switch t {
	case BankAccountCBU, BankAccountCVU, BankAccountIBAN:
		return t, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidAccountType, s)
}

func (t BankAccountType) String() string { return string(t) }

// ── TaxID ─────────────────────────────────────────────────────────────────────

// TaxID es el número de identificación tributaria del negocio.
// La validación de formato depende del país (ParseTaxIDForCountry); la
// verificación contra el organismo fiscal (AFIP/SII/RFB) queda fuera de alcance.
type TaxID string

// ParseTaxID valida el formato genérico: solo dígitos, 8–20 caracteres.
func ParseTaxID(s string) (TaxID, error) {
	clean := stripTaxIDSeparators(s)
	if len(clean) < 8 || len(clean) > 20 {
		return "", ErrInvalidTaxID
	}
	for _, r := range clean {
		if !unicode.IsDigit(r) {
			return "", ErrInvalidTaxID
		}
	}
	return TaxID(clean), nil
}

// ParseTaxIDForCountry valida el identificador fiscal según el país del
// domicilio (ISO 3166-1 alpha-2):
//
//   - AR: CUIT/CUIL de 11 dígitos con dígito verificador (módulo 11).
//   - Resto: alfanumérico de 5–20 caracteres (RFC, RUT, EIN, VAT, etc.),
//     normalizado a mayúsculas.
func ParseTaxIDForCountry(s, country string) (TaxID, error) {
	clean := strings.ToUpper(stripTaxIDSeparators(s))
	if strings.EqualFold(country, "AR") {
		if !isValidCUIT(clean) {
			return "", fmt.Errorf("%w: CUIT must have 11 digits and a valid check digit", ErrInvalidTaxID)
		}
		return TaxID(clean), nil
	}
	if len(clean) < 5 || len(clean) > 20 {
		return "", ErrInvalidTaxID
	}
	for _, r := range clean {
		if !unicode.IsDigit(r) && (r < 'A' || r > 'Z') {
			return "", ErrInvalidTaxID
		}
	}
	return TaxID(clean), nil
}

func stripTaxIDSeparators(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '.' || r == '/' || r == ' ' {
			return -1 // eliminar separadores comunes
		}
		return r
	}, s)
}

// isValidCUIT verifica el dígito verificador de un CUIT/CUIL (algoritmo AFIP:
// pesos 5,4,3,2,7,6,5,4,3,2; DV = 11 - suma%11, con 11 → 0 y 10 inválido).
func isValidCUIT(s string) bool {
	if len(s) != 11 {
		return false
	}
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		if i < 10 {
			sum += int(r-'0') * weights[i]
		}
	}
	dv := 11 - sum%11
	switch dv {
	case 11:
		dv = 0
	case 10:
		return false
	}
	return int(s[10]-'0') == dv
}

// ParseCurrency valida un código ISO 4217 de 3 letras y lo normaliza a mayúsculas.
func ParseCurrency(s string) (string, error) {
	cur := strings.ToUpper(strings.TrimSpace(s))
	if len(cur) != 3 {
		return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
	}
	for _, r := range cur {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
		}
	}
	return cur, nil
}

// ── ReviewDecision ────────────────────────────────────────────────────────────

// ReviewDecision es la acción del operador sobre una solicitud.
type ReviewDecision string

const (
	DecisionApprove         ReviewDecision = "approve"
	DecisionReject          ReviewDecision = "reject"
	DecisionRequestMoreInfo ReviewDecision = "request_more_info"
	// DecisionReopen reabre una solicitud rechazada (rejected → requires_more_info).
	DecisionReopen ReviewDecision = "reopen"
)

func ParseReviewDecision(s string) (ReviewDecision, error) {
	d := ReviewDecision(s)
	switch d {
	case DecisionApprove, DecisionReject, DecisionRequestMoreInfo, DecisionReopen:
		return d, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidDecision, s)
}

func (t TaxID) String() string { return string(t) }

// ── Address ───────────────────────────────────────────────────────────────────

// Address es el domicilio fiscal del comercio.
type Address struct {
	Street     string
	City       string
	State      string
	Country    string // ISO 3166-1 alpha-2: "AR", "US"
	PostalCode string
}

func (a Address) IsComplete() bool {
	return a.Street != "" && a.City != "" && a.Country != ""
}

// ── BusinessInfo ──────────────────────────────────────────────────────────────

// BusinessInfo agrupa los datos legales del negocio.
type BusinessInfo struct {
	LegalName        string
	TaxID            TaxID
	BusinessCategory BusinessCategory
	Address          Address
	Website          string
	PhoneNumber      string
}

func (b BusinessInfo) IsComplete() bool {
	return b.LegalName != "" &&
		b.TaxID != "" &&
		b.BusinessCategory != "" &&
		b.Address.IsComplete()
}
