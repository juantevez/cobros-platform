package domain

import "errors"

var (
	// ErrAlertNotFound indica que la alerta no existe o no pertenece al tenant.
	ErrAlertNotFound = errors.New("compliance: alert not found")
	// ErrDuplicateAlert indica que ya existe una alerta para (tenant, tipo, subject).
	ErrDuplicateAlert = errors.New("compliance: duplicate alert")
	// ErrAlertNotOpen indica que la alerta ya fue resuelta y no puede re-resolverse.
	ErrAlertNotOpen = errors.New("compliance: alert is not open")
	// ErrInvalidDisposition indica una disposición de revisión inválida.
	ErrInvalidDisposition = errors.New("compliance: invalid disposition")
)
