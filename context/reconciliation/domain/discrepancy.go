package domain

import (
	"fmt"
	"time"
)

// Discrepancy representa una inconsistencia encontrada durante un run.
//
// Almacena los valores de ambos lados (sistema y externo) para facilitar
// la investigación por parte del operador. Una vez investigada, se marca
// como Resolved (corregida) o Ignored (conocida y aceptada).
type Discrepancy struct {
	id            DiscrepancyID
	runID         RunID
	tenantID      TenantID
	discType      DiscrepancyType
	recordID      string // psp_reference, payment_id, account_id según el tipo
	systemValue   string // lo que dice el sistema (JSON o string descriptivo)
	externalValue string // lo que dice el PSP/banco/informe externo
	status        DiscrepancyStatus
	notes         string
	resolvedBy    string
	resolvedAt    *time.Time
	createdAt     time.Time
}

// NewDiscrepancy crea una discrepancia encontrada durante la comparación.
func NewDiscrepancy(
	id DiscrepancyID,
	runID RunID,
	tenantID TenantID,
	discType DiscrepancyType,
	recordID string,
	systemValue string,
	externalValue string,
) *Discrepancy {
	return &Discrepancy{
		id:            id,
		runID:         runID,
		tenantID:      tenantID,
		discType:      discType,
		recordID:      recordID,
		systemValue:   systemValue,
		externalValue: externalValue,
		status:        DiscrepancyOpen,
		createdAt:     time.Now().UTC(),
	}
}

// ReconstituteDiscrepancy reconstruye desde el repositorio.
func ReconstituteDiscrepancy(
	id DiscrepancyID, runID RunID, tenantID TenantID,
	discType DiscrepancyType, recordID, systemValue, externalValue string,
	status DiscrepancyStatus, notes, resolvedBy string,
	resolvedAt *time.Time, createdAt time.Time,
) *Discrepancy {
	return &Discrepancy{
		id: id, runID: runID, tenantID: tenantID,
		discType: discType, recordID: recordID,
		systemValue: systemValue, externalValue: externalValue,
		status: status, notes: notes,
		resolvedBy: resolvedBy, resolvedAt: resolvedAt,
		createdAt: createdAt,
	}
}

// Resolve marca la discrepancia como resuelta.
func (d *Discrepancy) Resolve(resolvedBy, notes string) error {
	if d.status != DiscrepancyOpen {
		return fmt.Errorf("%w: status is %q", ErrDiscrepancyAlreadyResolved, d.status)
	}
	now := time.Now().UTC()
	d.status = DiscrepancyResolved
	d.resolvedBy = resolvedBy
	d.notes = notes
	d.resolvedAt = &now
	return nil
}

// Ignore marca la discrepancia como conocida y aceptada (no requiere corrección).
func (d *Discrepancy) Ignore(resolvedBy, notes string) error {
	if d.status != DiscrepancyOpen {
		return fmt.Errorf("%w: status is %q", ErrDiscrepancyAlreadyResolved, d.status)
	}
	now := time.Now().UTC()
	d.status = DiscrepancyIgnored
	d.resolvedBy = resolvedBy
	d.notes = notes
	d.resolvedAt = &now
	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (d *Discrepancy) ID() DiscrepancyID           { return d.id }
func (d *Discrepancy) RunID() RunID                { return d.runID }
func (d *Discrepancy) TenantID() TenantID          { return d.tenantID }
func (d *Discrepancy) Type() DiscrepancyType       { return d.discType }
func (d *Discrepancy) RecordID() string            { return d.recordID }
func (d *Discrepancy) SystemValue() string         { return d.systemValue }
func (d *Discrepancy) ExternalValue() string       { return d.externalValue }
func (d *Discrepancy) Status() DiscrepancyStatus   { return d.status }
func (d *Discrepancy) Notes() string               { return d.notes }
func (d *Discrepancy) ResolvedBy() string          { return d.resolvedBy }
func (d *Discrepancy) ResolvedAt() *time.Time      { return d.resolvedAt }
func (d *Discrepancy) CreatedAt() time.Time        { return d.createdAt }
