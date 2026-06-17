package domain

import "errors"

var (
	ErrRunNotFound            = errors.New("reconciliation run not found")
	ErrRunAlreadyRunning      = errors.New("reconciliation run is already in progress")
	ErrRunNotCompleted        = errors.New("reconciliation run is not completed yet")
	ErrDiscrepancyNotFound    = errors.New("discrepancy not found")
	ErrDiscrepancyAlreadyResolved = errors.New("discrepancy is already resolved")
	ErrInvalidPeriod          = errors.New("period_from must be before period_to")
	ErrEmptyReport            = errors.New("report data is empty")
	ErrInvalidReportFormat    = errors.New("report format is invalid or unrecognized")
)
