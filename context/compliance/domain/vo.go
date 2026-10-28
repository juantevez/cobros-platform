// Package domain define el núcleo del contexto Compliance & AML:
// screening contra listas de vigilancia y monitoreo de transacciones.
package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type AlertID string
type TenantID string

// Errores tipados de los value objects: permiten a la capa HTTP distinguir
// entrada inválida (400) de fallos internos (500) sin comparar cadenas.
var (
	ErrInvalidTenantID = errors.New("compliance: invalid tenant id")
	ErrInvalidAlertID  = errors.New("compliance: invalid alert id")
)

func NewAlertID() AlertID { return AlertID(uuid.NewString()) }

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidTenantID, s)
	}
	return TenantID(s), nil
}

func ParseAlertID(s string) (AlertID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidAlertID, s)
	}
	return AlertID(s), nil
}

func (id AlertID) String() string  { return string(id) }
func (id TenantID) String() string { return string(id) }

// ── AlertType ─────────────────────────────────────────────────────────────────

type AlertType string

const (
	AlertSanctionsMatch       AlertType = "sanctions_match"
	AlertTransactionThreshold AlertType = "transaction_threshold"
	AlertTransactionVelocity  AlertType = "transaction_velocity"
	AlertTransactionVolume    AlertType = "transaction_volume"
)

func (t AlertType) String() string { return string(t) }

// ── RiskLevel ─────────────────────────────────────────────────────────────────

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

func (r RiskLevel) String() string { return string(r) }

// ── AlertStatus ───────────────────────────────────────────────────────────────

type AlertStatus string

const (
	StatusOpen      AlertStatus = "open"
	StatusCleared   AlertStatus = "cleared"   // revisado y descartado (falso positivo)
	StatusConfirmed AlertStatus = "confirmed" // revisado y confirmado (verdadero positivo)
)

func (s AlertStatus) String() string { return string(s) }

// ParseStatusFilter valida el filtro de estado de un listado. Vacío = sin filtro.
func ParseStatusFilter(s string) (AlertStatus, error) {
	switch AlertStatus(s) {
	case "", StatusOpen, StatusCleared, StatusConfirmed:
		return AlertStatus(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidStatusFilter, s)
}

// ParseDisposition mapea la disposición de un analista a un estado terminal.
func ParseDisposition(s string) (AlertStatus, error) {
	switch AlertStatus(s) {
	case StatusCleared:
		return StatusCleared, nil
	case StatusConfirmed:
		return StatusConfirmed, nil
	}
	return "", fmt.Errorf("%w: %q (use cleared|confirmed)", ErrInvalidDisposition, s)
}
