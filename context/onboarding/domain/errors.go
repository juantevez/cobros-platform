package domain

import "errors"

var (
	ErrApplicationNotFound   = errors.New("onboarding application not found")
	ErrApplicationExists     = errors.New("tenant already has an onboarding application")
	ErrInvalidTransition     = errors.New("invalid status transition for onboarding application")
	ErrIncompleteApplication = errors.New("application is incomplete: missing required information")
	ErrInvalidTaxID          = errors.New("invalid tax id format")
	ErrInvalidBusinessCat    = errors.New("invalid business category")
	ErrInvalidDocumentType   = errors.New("invalid document type")
	ErrInvalidPersonRole     = errors.New("invalid person role")
	ErrInvalidAccountType    = errors.New("invalid bank account type")
	ErrRejectionReasonEmpty  = errors.New("rejection reason is required")
	ErrReviewNotesEmpty      = errors.New("review notes are required when requesting more info")
)
