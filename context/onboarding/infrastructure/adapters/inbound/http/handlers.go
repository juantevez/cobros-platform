package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/onboarding/application"
	"github.com/juantevez/cobros-platform/context/onboarding/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// OnboardingHandler maneja el flujo del comercio.
type OnboardingHandler struct {
	submit       *application.SubmitApplicationUseCase
	uploadDoc    *application.UploadDocumentUseCase
	addPerson    *application.AddPersonUseCase
	setBankAcct  *application.SetBankAccountUseCase
	submitReview *application.SubmitForReviewUseCase
	getApp       *application.GetApplicationUseCase
}

func NewOnboardingHandler(
	submit *application.SubmitApplicationUseCase,
	uploadDoc *application.UploadDocumentUseCase,
	addPerson *application.AddPersonUseCase,
	setBankAcct *application.SetBankAccountUseCase,
	submitReview *application.SubmitForReviewUseCase,
	getApp *application.GetApplicationUseCase,
) *OnboardingHandler {
	return &OnboardingHandler{
		submit: submit, uploadDoc: uploadDoc, addPerson: addPerson,
		setBankAcct: setBankAcct, submitReview: submitReview, getApp: getApp,
	}
}

// ReviewHandler maneja las acciones del operador.
type ReviewHandler struct {
	review *application.ReviewApplicationUseCase
}

func NewReviewHandler(review *application.ReviewApplicationUseCase) *ReviewHandler {
	return &ReviewHandler{review: review}
}

// ── Endpoints del comercio ────────────────────────────────────────────────────

type submitApplicationReq struct {
	LegalName        string `json:"legal_name"        binding:"required"`
	TaxID            string `json:"tax_id"            binding:"required"`
	BusinessCategory string `json:"business_category" binding:"required"`
	Street           string `json:"street"`
	City             string `json:"city"              binding:"required"`
	State            string `json:"state"`
	Country          string `json:"country"           binding:"required"`
	PostalCode       string `json:"postal_code"`
	Website          string `json:"website"`
	PhoneNumber      string `json:"phone_number"`
}

// Submit	POST /api/v1/onboarding
func (h *OnboardingHandler) Submit(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req submitApplicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	result, err := h.submit.Execute(c.Request.Context(), application.SubmitApplicationCmd{
		TenantID: tenantID, LegalName: req.LegalName, TaxID: req.TaxID,
		BusinessCategory: req.BusinessCategory, Street: req.Street, City: req.City,
		State: req.State, Country: req.Country, PostalCode: req.PostalCode,
		Website: req.Website, PhoneNumber: req.PhoneNumber,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"application_id": result.ApplicationID})
}

// Get	GET /api/v1/onboarding
func (h *OnboardingHandler) Get(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	view, err := h.getApp.Execute(c.Request.Context(), application.GetApplicationQuery{TenantID: tenantID})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

type uploadDocReq struct {
	DocumentType string `json:"document_type" binding:"required"`
	Reference    string `json:"reference"     binding:"required"`
}

// UploadDocument	POST /api/v1/onboarding/documents
func (h *OnboardingHandler) UploadDocument(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req uploadDocReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.uploadDoc.Execute(c.Request.Context(), application.UploadDocumentCmd{
		TenantID: tenantID, DocumentType: req.DocumentType, Reference: req.Reference,
	}); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusCreated)
}

type addPersonReq struct {
	FullName          string `json:"full_name"            binding:"required"`
	Role              string `json:"role"                 binding:"required"`
	IdentityDocType   string `json:"identity_doc_type"`
	IdentityDocNumber string `json:"identity_doc_number"`
	Nationality       string `json:"nationality"`
}

// AddPerson	POST /api/v1/onboarding/persons
func (h *OnboardingHandler) AddPerson(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req addPersonReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.addPerson.Execute(c.Request.Context(), application.AddPersonCmd{
		TenantID: tenantID, FullName: req.FullName, Role: req.Role,
		IdentityDocType: req.IdentityDocType, IdentityDocNumber: req.IdentityDocNumber,
		Nationality: req.Nationality,
	}); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusCreated)
}

type setBankAccountReq struct {
	AccountType   string `json:"account_type"   binding:"required"`
	AccountNumber string `json:"account_number" binding:"required"`
	BankName      string `json:"bank_name"`
	HolderName    string `json:"holder_name"    binding:"required"`
	Currency      string `json:"currency"       binding:"required"`
}

// SetBankAccount	PUT /api/v1/onboarding/bank-account
func (h *OnboardingHandler) SetBankAccount(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	var req setBankAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.setBankAcct.Execute(c.Request.Context(), application.SetBankAccountCmd{
		TenantID: tenantID, AccountType: req.AccountType, AccountNumber: req.AccountNumber,
		BankName: req.BankName, HolderName: req.HolderName, Currency: req.Currency,
	}); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SubmitForReview	POST /api/v1/onboarding/submit
func (h *OnboardingHandler) SubmitForReview(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())
	if err := h.submitReview.Execute(c.Request.Context(), application.SubmitForReviewCmd{
		TenantID: tenantID,
	}); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Endpoints del operador ───────────────────────────────────────────────────

type reviewReq struct {
	Decision string `json:"decision" binding:"required"`
	Notes    string `json:"notes"`
}

// Review	POST /api/v1/onboarding/applications/:id/review
func (h *ReviewHandler) Review(c *gin.Context) {
	var req reviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.review.Execute(c.Request.Context(), application.ReviewApplicationCmd{
		ApplicationID: c.Param("id"),
		Decision:      req.Decision,
		Notes:         req.Notes,
	}); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func respondDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrApplicationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrApplicationExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrIncompleteApplication):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidTaxID),
		errors.Is(err, domain.ErrInvalidBusinessCat),
		errors.Is(err, domain.ErrInvalidDocumentType),
		errors.Is(err, domain.ErrInvalidPersonRole),
		errors.Is(err, domain.ErrInvalidAccountType),
		errors.Is(err, domain.ErrRejectionReasonEmpty),
		errors.Is(err, domain.ErrReviewNotesEmpty):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// RegisterRoutes registra las rutas de onboarding en el grupo protegido por JWT.
func RegisterRoutes(rg *gin.RouterGroup, onboarding *OnboardingHandler, review *ReviewHandler) {
	ob := rg.Group("/onboarding")
	{
		ob.POST("", onboarding.Submit)
		ob.GET("", onboarding.Get)
		ob.POST("/documents", onboarding.UploadDocument)
		ob.POST("/persons", onboarding.AddPerson)
		ob.PUT("/bank-account", onboarding.SetBankAccount)
		ob.POST("/submit", onboarding.SubmitForReview)
		ob.POST("/applications/:id/review", review.Review)
	}
}
