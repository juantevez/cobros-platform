package domain

import "time"

// ── BusinessDocument ──────────────────────────────────────────────────────────

// DocumentStatus es el estado de revisión de un documento.
type DocumentStatus string

const (
	DocStatusPending  DocumentStatus = "pending"
	DocStatusVerified DocumentStatus = "verified"
	DocStatusRejected DocumentStatus = "rejected"
)

// BusinessDocument es un documento cargado por el comercio.
// La referencia es un identificador externo (URL de S3, ID de un servicio, etc.).
type BusinessDocument struct {
	id           DocumentID
	documentType DocumentType
	reference    string // referencia externa al documento almacenado
	status       DocumentStatus
	notes        string // notas del revisor
	uploadedAt   time.Time
}

func NewBusinessDocument(id DocumentID, docType DocumentType, reference string) BusinessDocument {
	return BusinessDocument{
		id:           id,
		documentType: docType,
		reference:    reference,
		status:       DocStatusPending,
		uploadedAt:   time.Now().UTC(),
	}
}

func ReconstituteDocument(id DocumentID, docType DocumentType, reference, notes string, status DocumentStatus, uploadedAt time.Time) BusinessDocument {
	return BusinessDocument{id: id, documentType: docType, reference: reference, status: status, notes: notes, uploadedAt: uploadedAt}
}

func (d BusinessDocument) ID() DocumentID         { return d.id }
func (d BusinessDocument) DocumentType() DocumentType { return d.documentType }
func (d BusinessDocument) Reference() string      { return d.reference }
func (d BusinessDocument) Status() DocumentStatus { return d.status }
func (d BusinessDocument) Notes() string          { return d.notes }
func (d BusinessDocument) UploadedAt() time.Time  { return d.uploadedAt }

// ── Person ────────────────────────────────────────────────────────────────────

// Person es un titular, director o beneficiario final (UBO) del negocio.
type Person struct {
	id                 PersonID
	fullName           string
	role               PersonRole
	identityDocType    string // ej: "DNI", "Pasaporte"
	identityDocNumber  string
	nationality        string // ISO 3166-1 alpha-2
	createdAt          time.Time
}

func NewPerson(id PersonID, fullName string, role PersonRole, identityDocType, identityDocNumber, nationality string) Person {
	return Person{
		id:                id,
		fullName:          fullName,
		role:              role,
		identityDocType:   identityDocType,
		identityDocNumber: identityDocNumber,
		nationality:       nationality,
		createdAt:         time.Now().UTC(),
	}
}

func ReconstitutePerson(id PersonID, fullName string, role PersonRole, identityDocType, identityDocNumber, nationality string, createdAt time.Time) Person {
	return Person{id: id, fullName: fullName, role: role, identityDocType: identityDocType, identityDocNumber: identityDocNumber, nationality: nationality, createdAt: createdAt}
}

func (p Person) ID() PersonID                  { return p.id }
func (p Person) FullName() string              { return p.fullName }
func (p Person) Role() PersonRole              { return p.role }
func (p Person) IdentityDocType() string       { return p.identityDocType }
func (p Person) IdentityDocNumber() string     { return p.identityDocNumber }
func (p Person) Nationality() string           { return p.nationality }
func (p Person) CreatedAt() time.Time          { return p.createdAt }

// ── BankAccount ───────────────────────────────────────────────────────────────

// BankAccount es la cuenta bancaria del comercio para recibir desembolsos.
type BankAccount struct {
	id            BankAccountID
	accountType   BankAccountType // CBU, CVU, IBAN
	accountNumber string
	bankName      string
	holderName    string
	currency      string // ISO 4217
	verified      bool
	createdAt     time.Time
}

func NewBankAccount(id BankAccountID, accountType BankAccountType, accountNumber, bankName, holderName, currency string) BankAccount {
	return BankAccount{
		id:            id,
		accountType:   accountType,
		accountNumber: accountNumber,
		bankName:      bankName,
		holderName:    holderName,
		currency:      currency,
		verified:      false,
		createdAt:     time.Now().UTC(),
	}
}

func ReconstituteBankAccount(id BankAccountID, accountType BankAccountType, accountNumber, bankName, holderName, currency string, verified bool, createdAt time.Time) BankAccount {
	return BankAccount{id: id, accountType: accountType, accountNumber: accountNumber, bankName: bankName, holderName: holderName, currency: currency, verified: verified, createdAt: createdAt}
}

func (b BankAccount) ID() BankAccountID          { return b.id }
func (b BankAccount) AccountType() BankAccountType { return b.accountType }
func (b BankAccount) AccountNumber() string      { return b.accountNumber }
func (b BankAccount) BankName() string           { return b.bankName }
func (b BankAccount) HolderName() string         { return b.holderName }
func (b BankAccount) Currency() string           { return b.currency }
func (b BankAccount) Verified() bool             { return b.verified }
func (b BankAccount) CreatedAt() time.Time       { return b.createdAt }
