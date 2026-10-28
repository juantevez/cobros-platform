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
	// ErrInvalidListType indica un tipo de lista de vigilancia inválido.
	ErrInvalidListType = errors.New("compliance: invalid list type, must be sanctions or pep")
	// ErrInvalidWatchlistName indica un nombre vacío (o sin letras/dígitos) en la watchlist.
	ErrInvalidWatchlistName = errors.New("compliance: watchlist full_name must contain letters or digits")
	// ErrWatchlistEntryNotFound indica que la entrada no existe o ya fue dada de baja.
	ErrWatchlistEntryNotFound = errors.New("compliance: watchlist entry not found")
	// ErrInvalidStatusFilter indica un filtro de estado desconocido al listar alertas.
	ErrInvalidStatusFilter = errors.New("compliance: invalid status filter (use open|cleared|confirmed)")
)
