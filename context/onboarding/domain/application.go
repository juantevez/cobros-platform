package domain

import (
	"fmt"
	"time"
)

// OnboardingApplication es el agregado raíz del contexto Onboarding.
//
// Gestiona el proceso de validación de identidad (KYC/KYB) de un comercio.
// La máquina de estados governa qué operaciones están permitidas en cada momento:
//
//	pending ──────────────── SubmitForReview ──→ in_review
//	                                                │
//	requires_more_info ←── RequestMoreInfo ─────────┤
//	       │                                        ├── Approve ──→ approved (final)
//	       └──── SubmitForReview ──→ in_review      └── Reject  ──→ rejected  (final)
//
// Cuando pasa a approved, emite ApplicationApprovedEvent que activa el Tenant
// en Auth y crea las cuentas contables en Ledger.
type OnboardingApplication struct {
	id              ApplicationID
	tenantID        TenantID
	status          ApplicationStatus
	businessInfo    BusinessInfo
	bankAccount     *BankAccount
	documents       []BusinessDocument
	persons         []Person
	reviewNotes     string
	rejectionReason string
	submittedAt     *time.Time
	reviewedAt      *time.Time
	createdAt       time.Time
	updatedAt       time.Time

	events []Event
}

// NewOnboardingApplication crea una solicitud de onboarding en estado Pending.
func NewOnboardingApplication(id ApplicationID, tenantID TenantID, info BusinessInfo) (*OnboardingApplication, error) {
	if info.LegalName == "" {
		return nil, fmt.Errorf("legal name is required to start onboarding")
	}

	now := time.Now().UTC()
	a := &OnboardingApplication{
		id:           id,
		tenantID:     tenantID,
		status:       StatusPending,
		businessInfo: info,
		createdAt:    now,
		updatedAt:    now,
	}

	a.record(ApplicationSubmittedEvent{
		baseEvent:     newBase(tenantID.String()),
		ApplicationID: id.String(),
		TenantID:      tenantID.String(),
		LegalName:     info.LegalName,
	})

	return a, nil
}

// ReconstituteOnboardingApplication reconstruye el agregado desde el repositorio.
func ReconstituteOnboardingApplication(
	id ApplicationID, tenantID TenantID,
	status ApplicationStatus,
	businessInfo BusinessInfo,
	bankAccount *BankAccount,
	documents []BusinessDocument,
	persons []Person,
	reviewNotes, rejectionReason string,
	submittedAt, reviewedAt *time.Time,
	createdAt, updatedAt time.Time,
) *OnboardingApplication {
	return &OnboardingApplication{
		id: id, tenantID: tenantID, status: status,
		businessInfo: businessInfo, bankAccount: bankAccount,
		documents: documents, persons: persons,
		reviewNotes: reviewNotes, rejectionReason: rejectionReason,
		submittedAt: submittedAt, reviewedAt: reviewedAt,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// ── Mutaciones del comercio ───────────────────────────────────────────────────

// AddDocument agrega un documento a la solicitud.
// Solo permitido cuando la aplicación es editable.
func (a *OnboardingApplication) AddDocument(doc BusinessDocument) error {
	if !a.status.IsEditable() {
		return fmt.Errorf("%w: cannot add documents in status %q", ErrInvalidTransition, a.status)
	}
	a.documents = append(a.documents, doc)
	a.updatedAt = time.Now().UTC()
	return nil
}

// AddPerson agrega un titular, director o UBO.
func (a *OnboardingApplication) AddPerson(person Person) error {
	if !a.status.IsEditable() {
		return fmt.Errorf("%w: cannot add persons in status %q", ErrInvalidTransition, a.status)
	}
	a.persons = append(a.persons, person)
	a.updatedAt = time.Now().UTC()
	return nil
}

// SetBankAccount define la cuenta bancaria para desembolsos.
// Reemplaza la anterior si ya existía.
func (a *OnboardingApplication) SetBankAccount(account BankAccount) error {
	if !a.status.IsEditable() {
		return fmt.Errorf("%w: cannot set bank account in status %q", ErrInvalidTransition, a.status)
	}
	a.bankAccount = &account
	a.updatedAt = time.Now().UTC()
	return nil
}

// SubmitForReview envía la solicitud para revisión por el operador.
// Valida que la aplicación está completa antes de cambiar el estado.
func (a *OnboardingApplication) SubmitForReview() error {
	if !a.status.IsEditable() {
		return fmt.Errorf("%w: cannot submit in status %q", ErrInvalidTransition, a.status)
	}
	if err := a.validateCompleteness(); err != nil {
		return err
	}

	now := time.Now().UTC()
	a.status = StatusInReview
	a.submittedAt = &now
	a.updatedAt = now

	a.record(ApplicationSentForReviewEvent{
		baseEvent:     newBase(a.tenantID.String()),
		ApplicationID: a.id.String(),
		TenantID:      a.tenantID.String(),
	})

	return nil
}

// ── Mutaciones del operador ───────────────────────────────────────────────────

// Approve aprueba la solicitud. Solo desde in_review.
// Emite ApplicationApprovedEvent que activa el Tenant y crea cuentas Ledger.
func (a *OnboardingApplication) Approve(notes string) error {
	if a.status != StatusInReview {
		return fmt.Errorf("%w: cannot approve from status %q", ErrInvalidTransition, a.status)
	}

	now := time.Now().UTC()
	a.status = StatusApproved
	a.reviewNotes = notes
	a.reviewedAt = &now
	a.updatedAt = now

	currency := "ARS" // default Argentina; en Fase 3 configurable por comercio
	if a.bankAccount != nil && a.bankAccount.Currency() != "" {
		currency = a.bankAccount.Currency()
	}

	a.record(ApplicationApprovedEvent{
		baseEvent:        newBase(a.tenantID.String()),
		ApplicationID:    a.id.String(),
		TenantID:         a.tenantID.String(),
		BusinessCategory: a.businessInfo.BusinessCategory.String(),
		Currency:         currency,
	})

	return nil
}

// Reject rechaza la solicitud. Solo desde in_review.
func (a *OnboardingApplication) Reject(reason string) error {
	if a.status != StatusInReview {
		return fmt.Errorf("%w: cannot reject from status %q", ErrInvalidTransition, a.status)
	}
	if reason == "" {
		return ErrRejectionReasonEmpty
	}

	now := time.Now().UTC()
	a.status = StatusRejected
	a.rejectionReason = reason
	a.reviewedAt = &now
	a.updatedAt = now

	a.record(ApplicationRejectedEvent{
		baseEvent:       newBase(a.tenantID.String()),
		ApplicationID:   a.id.String(),
		TenantID:        a.tenantID.String(),
		RejectionReason: reason,
	})

	return nil
}

// RequestMoreInfo pide documentación adicional. Solo desde in_review.
// Vuelve al estado requires_more_info para que el comercio pueda continuar.
func (a *OnboardingApplication) RequestMoreInfo(notes string) error {
	if a.status != StatusInReview {
		return fmt.Errorf("%w: cannot request info from status %q", ErrInvalidTransition, a.status)
	}
	if notes == "" {
		return ErrReviewNotesEmpty
	}

	a.status = StatusRequiresMoreInfo
	a.reviewNotes = notes
	a.updatedAt = time.Now().UTC()

	a.record(MoreInfoRequestedEvent{
		baseEvent:     newBase(a.tenantID.String()),
		ApplicationID: a.id.String(),
		TenantID:      a.tenantID.String(),
		Notes:         notes,
	})

	return nil
}

// ── Validación ────────────────────────────────────────────────────────────────

func (a *OnboardingApplication) validateCompleteness() error {
	if !a.businessInfo.IsComplete() {
		return fmt.Errorf("%w: business info is incomplete", ErrIncompleteApplication)
	}
	if len(a.documents) == 0 {
		return fmt.Errorf("%w: at least one document is required", ErrIncompleteApplication)
	}
	if len(a.persons) == 0 {
		return fmt.Errorf("%w: at least one person (owner/director/UBO) is required", ErrIncompleteApplication)
	}
	if a.bankAccount == nil {
		return fmt.Errorf("%w: bank account is required", ErrIncompleteApplication)
	}
	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (a *OnboardingApplication) ID() ApplicationID              { return a.id }
func (a *OnboardingApplication) TenantID() TenantID             { return a.tenantID }
func (a *OnboardingApplication) Status() ApplicationStatus      { return a.status }
func (a *OnboardingApplication) BusinessInfo() BusinessInfo     { return a.businessInfo }
func (a *OnboardingApplication) BankAccount() *BankAccount      { return a.bankAccount }
func (a *OnboardingApplication) Documents() []BusinessDocument  { return a.documents }
func (a *OnboardingApplication) Persons() []Person              { return a.persons }
func (a *OnboardingApplication) ReviewNotes() string            { return a.reviewNotes }
func (a *OnboardingApplication) RejectionReason() string        { return a.rejectionReason }
func (a *OnboardingApplication) SubmittedAt() *time.Time        { return a.submittedAt }
func (a *OnboardingApplication) ReviewedAt() *time.Time         { return a.reviewedAt }
func (a *OnboardingApplication) CreatedAt() time.Time           { return a.createdAt }
func (a *OnboardingApplication) UpdatedAt() time.Time           { return a.updatedAt }

func (a *OnboardingApplication) PullEvents() []Event {
	evs := a.events
	a.events = nil
	return evs
}

func (a *OnboardingApplication) record(e Event) {
	a.events = append(a.events, e)
}
