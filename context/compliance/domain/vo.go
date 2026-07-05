// Package domain define el núcleo del contexto Compliance & AML:
// screening contra listas de vigilancia y monitoreo de transacciones.
package domain

import (
	"fmt"

	"github.com/google/uuid"
)

type AlertID string
type TenantID string

func NewAlertID() AlertID { return AlertID(uuid.NewString()) }

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func ParseAlertID(s string) (AlertID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid alert id: %w", err)
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
