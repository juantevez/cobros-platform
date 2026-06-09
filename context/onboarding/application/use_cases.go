package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

// ── SubmitApplication ─────────────────────────────────────────────────────────

// SubmitApplicationUseCase crea la solicitud inicial de onboarding.
// El comercio debe estar registrado en Auth (status: pending).
// Solo puede existir una solicitud por tenant.
type SubmitApplicationUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
	publisher EventPublisher
}

func NewSubmitApplicationUseCase(repo ApplicationRepository, txManager TxManager, publisher EventPublisher) *SubmitApplicationUseCase {
	return &SubmitApplicationUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *SubmitApplicationUseCase) Execute(ctx context.Context, cmd SubmitApplicationCmd) (SubmitApplicationResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return SubmitApplicationResult{}, err
	}

	taxID, err := domain.ParseTaxID(cmd.TaxID)
	if err != nil {
		return SubmitApplicationResult{}, err
	}

	category, err := domain.ParseBusinessCategory(cmd.BusinessCategory)
	if err != nil {
		return SubmitApplicationResult{}, err
	}

	// Un tenant solo puede tener una solicitud activa.
	existing, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err == nil && existing != nil {
		return SubmitApplicationResult{}, domain.ErrApplicationExists
	}

	info := domain.BusinessInfo{
		LegalName:        cmd.LegalName,
		TaxID:            taxID,
		BusinessCategory: category,
		Address: domain.Address{
			Street:     cmd.Street,
			City:       cmd.City,
			State:      cmd.State,
			Country:    cmd.Country,
			PostalCode: cmd.PostalCode,
		},
		Website:     cmd.Website,
		PhoneNumber: cmd.PhoneNumber,
	}

	id := domain.NewApplicationID()
	app, err := domain.NewOnboardingApplication(id, tenantID, info)
	if err != nil {
		return SubmitApplicationResult{}, err
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Save(ctx, app); err != nil {
			return fmt.Errorf("save application: %w", err)
		}
		return uc.publisher.Publish(ctx, app.PullEvents()...)
	}); err != nil {
		return SubmitApplicationResult{}, err
	}

	return SubmitApplicationResult{ApplicationID: id.String()}, nil
}

// ── UploadDocument ────────────────────────────────────────────────────────────

type UploadDocumentUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
}

func NewUploadDocumentUseCase(repo ApplicationRepository, txManager TxManager) *UploadDocumentUseCase {
	return &UploadDocumentUseCase{repo: repo, txManager: txManager}
}

func (uc *UploadDocumentUseCase) Execute(ctx context.Context, cmd UploadDocumentCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	docType, err := domain.ParseDocumentType(cmd.DocumentType)
	if err != nil {
		return err
	}
	if cmd.Reference == "" {
		return fmt.Errorf("document reference is required")
	}

	app, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	doc := domain.NewBusinessDocument(domain.NewDocumentID(), docType, cmd.Reference)
	if err := app.AddDocument(doc); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.repo.Update(ctx, app)
	})
}

// ── AddPerson ─────────────────────────────────────────────────────────────────

type AddPersonUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
}

func NewAddPersonUseCase(repo ApplicationRepository, txManager TxManager) *AddPersonUseCase {
	return &AddPersonUseCase{repo: repo, txManager: txManager}
}

func (uc *AddPersonUseCase) Execute(ctx context.Context, cmd AddPersonCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	if cmd.FullName == "" {
		return fmt.Errorf("full name is required")
	}

	role, err := domain.ParsePersonRole(cmd.Role)
	if err != nil {
		return err
	}

	app, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	person := domain.NewPerson(
		domain.NewPersonID(),
		cmd.FullName, role,
		cmd.IdentityDocType,
		cmd.IdentityDocNumber,
		cmd.Nationality,
	)

	if err := app.AddPerson(person); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.repo.Update(ctx, app)
	})
}

// ── SetBankAccount ────────────────────────────────────────────────────────────

type SetBankAccountUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
}

func NewSetBankAccountUseCase(repo ApplicationRepository, txManager TxManager) *SetBankAccountUseCase {
	return &SetBankAccountUseCase{repo: repo, txManager: txManager}
}

func (uc *SetBankAccountUseCase) Execute(ctx context.Context, cmd SetBankAccountCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	accountType, err := domain.ParseBankAccountType(cmd.AccountType)
	if err != nil {
		return err
	}
	if cmd.AccountNumber == "" || cmd.HolderName == "" {
		return fmt.Errorf("account number and holder name are required")
	}

	app, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	account := domain.NewBankAccount(
		domain.NewBankAccountID(),
		accountType,
		cmd.AccountNumber,
		cmd.BankName,
		cmd.HolderName,
		cmd.Currency,
	)

	if err := app.SetBankAccount(account); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.repo.Update(ctx, app)
	})
}

// ── SubmitForReview ───────────────────────────────────────────────────────────

type SubmitForReviewUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
	publisher EventPublisher
}

func NewSubmitForReviewUseCase(repo ApplicationRepository, txManager TxManager, publisher EventPublisher) *SubmitForReviewUseCase {
	return &SubmitForReviewUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *SubmitForReviewUseCase) Execute(ctx context.Context, cmd SubmitForReviewCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	app, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	if err := app.SubmitForReview(); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, app); err != nil {
			return fmt.Errorf("update application: %w", err)
		}
		return uc.publisher.Publish(ctx, app.PullEvents()...)
	})
}

// ── ReviewApplication ─────────────────────────────────────────────────────────

// ReviewApplicationUseCase permite al operador aprobar, rechazar o pedir más info.
// Solo usuarios con rol platform_support pueden ejecutar esto.
type ReviewApplicationUseCase struct {
	repo      ApplicationRepository
	txManager TxManager
	publisher EventPublisher
}

func NewReviewApplicationUseCase(repo ApplicationRepository, txManager TxManager, publisher EventPublisher) *ReviewApplicationUseCase {
	return &ReviewApplicationUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *ReviewApplicationUseCase) Execute(ctx context.Context, cmd ReviewApplicationCmd) error {
	appID, err := domain.ParseApplicationID(cmd.ApplicationID)
	if err != nil {
		return err
	}

	app, err := uc.repo.FindByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	switch cmd.Decision {
	case "approve":
		if err := app.Approve(cmd.Notes); err != nil {
			return err
		}
	case "reject":
		if err := app.Reject(cmd.Notes); err != nil {
			return err
		}
	case "request_more_info":
		if err := app.RequestMoreInfo(cmd.Notes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid decision %q: must be approve, reject, or request_more_info", cmd.Decision)
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, app); err != nil {
			return fmt.Errorf("update application: %w", err)
		}
		return uc.publisher.Publish(ctx, app.PullEvents()...)
	})
}

// ── GetApplication ────────────────────────────────────────────────────────────

type GetApplicationUseCase struct {
	repo ApplicationRepository
}

func NewGetApplicationUseCase(repo ApplicationRepository) *GetApplicationUseCase {
	return &GetApplicationUseCase{repo: repo}
}

func (uc *GetApplicationUseCase) Execute(ctx context.Context, q GetApplicationQuery) (ApplicationView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return ApplicationView{}, err
	}

	app, err := uc.repo.FindByTenantID(ctx, tenantID)
	if err != nil {
		return ApplicationView{}, err
	}

	return ApplicationView{
		ID:               app.ID().String(),
		TenantID:         app.TenantID().String(),
		Status:           app.Status().String(),
		LegalName:        app.BusinessInfo().LegalName,
		TaxID:            app.BusinessInfo().TaxID.String(),
		BusinessCategory: app.BusinessInfo().BusinessCategory.String(),
		DocumentCount:    len(app.Documents()),
		PersonCount:      len(app.Persons()),
		HasBankAccount:   app.BankAccount() != nil,
		ReviewNotes:      app.ReviewNotes(),
		RejectionReason:  app.RejectionReason(),
		SubmittedAt:      app.SubmittedAt(),
		ReviewedAt:       app.ReviewedAt(),
		CreatedAt:        app.CreatedAt(),
		UpdatedAt:        app.UpdatedAt(),
	}, nil
}
