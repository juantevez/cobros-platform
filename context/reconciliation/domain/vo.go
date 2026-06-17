package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type RunID string
type DiscrepancyID string
type TenantID string

func NewRunID() RunID               { return RunID(uuid.NewString()) }
func NewDiscrepancyID() DiscrepancyID { return DiscrepancyID(uuid.NewString()) }

func ParseRunID(s string) (RunID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid run id: %w", err)
	}
	return RunID(s), nil
}

func ParseDiscrepancyID(s string) (DiscrepancyID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid discrepancy id: %w", err)
	}
	return DiscrepancyID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id RunID) String() string          { return string(id) }
func (id DiscrepancyID) String() string  { return string(id) }
func (id TenantID) String() string       { return string(id) }

// ── ReconciliationType ────────────────────────────────────────────────────────

// ReconciliationType define qué se está reconciliando.
type ReconciliationType string

const (
	// TypePayment: pagos del sistema vs informe del PSP.
	TypePayment ReconciliationType = "payment"
	// TypeInternal: coherencia interna del Ledger (sum débitos = sum créditos).
	TypeInternal ReconciliationType = "internal_ledger"
)

func ParseReconciliationType(s string) (ReconciliationType, error) {
	t := ReconciliationType(s)
	switch t {
	case TypePayment, TypeInternal:
		return t, nil
	}
	return "", fmt.Errorf("invalid reconciliation type: %q", s)
}

func (t ReconciliationType) String() string { return string(t) }

// ── RunStatus ─────────────────────────────────────────────────────────────────

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"   // creado, esperando que se procese
	RunStatusRunning   RunStatus = "running"   // procesando el informe
	RunStatusCompleted RunStatus = "completed" // finalizado; ver discrepancias
	RunStatusFailed    RunStatus = "failed"    // error durante el procesamiento
)

func (s RunStatus) String() string { return string(s) }

// ── DiscrepancyType ───────────────────────────────────────────────────────────

// DiscrepancyType clasifica el tipo de inconsistencia encontrada.
type DiscrepancyType string

const (
	// MissingInPSP: el pago existe en el sistema pero el PSP no lo reporta.
	// Posible causa: el PSP perdió el registro o hubo un error de red.
	DiscrepancyMissingInPSP DiscrepancyType = "missing_in_psp"

	// MissingInSystem: el PSP reporta una transacción que no existe en el sistema.
	// Posible causa: el pago se procesó en el PSP pero no se persistió en la plataforma.
	DiscrepancyMissingInSystem DiscrepancyType = "missing_in_system"

	// AmountMismatch: el monto capturado difiere entre sistema y PSP.
	// Posible causa: fee del PSP aplicado incorrectamente, redondeo, fraude.
	DiscrepancyAmountMismatch DiscrepancyType = "amount_mismatch"

	// StatusMismatch: el estado difiere entre sistema y PSP.
	// Ejemplo: sistema dice "captured" pero PSP dice "rejected".
	DiscrepancyStatusMismatch DiscrepancyType = "status_mismatch"

	// LedgerImbalance: el Ledger no balancea (sum débitos ≠ sum créditos).
	// Indica un bug en la lógica de asientos o corrupción de datos.
	DiscrepancyLedgerImbalance DiscrepancyType = "ledger_imbalance"
)

func (t DiscrepancyType) String() string { return string(t) }

// ── DiscrepancyStatus ─────────────────────────────────────────────────────────

type DiscrepancyStatus string

const (
	DiscrepancyOpen     DiscrepancyStatus = "open"     // pendiente de investigación
	DiscrepancyResolved DiscrepancyStatus = "resolved" // investigada y resuelta
	DiscrepancyIgnored  DiscrepancyStatus = "ignored"  // conocida y aceptada
)

func (s DiscrepancyStatus) String() string { return string(s) }
