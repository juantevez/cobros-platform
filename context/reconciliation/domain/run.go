package domain

import (
	"fmt"
	"time"
)

// ReconciliationRun es el agregado raíz del contexto Reconciliation.
//
// Representa una ejecución del proceso de reconciliación para un período dado.
// La FSM es: pending → running → completed / failed
//
// Al completarse, contiene el resumen de cuántos registros coincidieron y
// cuántos generaron discrepancias. Las discrepancias se almacenan separadas.
type ReconciliationRun struct {
	id               RunID
	tenantID         TenantID // vacío para reconciliaciones globales de plataforma
	reconcType       ReconciliationType
	status           RunStatus
	periodFrom       time.Time
	periodTo         time.Time
	totalRecords     int // registros comparados
	matchedCount     int // registros que coinciden exactamente
	discrepancyCount int // registros con diferencias
	errorMsg         string
	createdAt        time.Time
	completedAt      *time.Time

	events []Event
}

// NewReconciliationRun crea un run en estado Pending.
func NewReconciliationRun(
	id RunID,
	tenantID TenantID,
	reconcType ReconciliationType,
	periodFrom, periodTo time.Time,
) (*ReconciliationRun, error) {
	if !periodFrom.Before(periodTo) {
		return nil, ErrInvalidPeriod
	}

	return &ReconciliationRun{
		id:         id,
		tenantID:   tenantID,
		reconcType: reconcType,
		status:     RunStatusPending,
		periodFrom: periodFrom,
		periodTo:   periodTo,
		createdAt:  time.Now().UTC(),
	}, nil
}

// ReconstituteRun reconstruye desde el repositorio.
func ReconstituteRun(
	id RunID, tenantID TenantID,
	reconcType ReconciliationType, status RunStatus,
	periodFrom, periodTo time.Time,
	totalRecords, matchedCount, discrepancyCount int,
	errorMsg string, createdAt time.Time, completedAt *time.Time,
) *ReconciliationRun {
	return &ReconciliationRun{
		id: id, tenantID: tenantID,
		reconcType: reconcType, status: status,
		periodFrom: periodFrom, periodTo: periodTo,
		totalRecords: totalRecords, matchedCount: matchedCount,
		discrepancyCount: discrepancyCount,
		errorMsg: errorMsg, createdAt: createdAt, completedAt: completedAt,
	}
}

// Start marca el run como en ejecución.
func (r *ReconciliationRun) Start() error {
	if r.status != RunStatusPending {
		return fmt.Errorf("%w: current status is %q", ErrRunAlreadyRunning, r.status)
	}
	r.status = RunStatusRunning
	return nil
}

// Complete finaliza el run con los resultados del proceso.
func (r *ReconciliationRun) Complete(totalRecords, matchedCount, discrepancyCount int) {
	now := time.Now().UTC()
	r.status = RunStatusCompleted
	r.totalRecords = totalRecords
	r.matchedCount = matchedCount
	r.discrepancyCount = discrepancyCount
	r.completedAt = &now

	r.record(ReconciliationCompletedEvent{
		baseEvent:        newBase(r.tenantID.String()),
		RunID:            r.id.String(),
		TenantID:         r.tenantID.String(),
		Type:             r.reconcType.String(),
		TotalRecords:     totalRecords,
		MatchedCount:     matchedCount,
		DiscrepancyCount: discrepancyCount,
	})
}

// Fail marca el run como fallido con el motivo del error.
func (r *ReconciliationRun) Fail(reason string) {
	now := time.Now().UTC()
	r.status = RunStatusFailed
	r.errorMsg = reason
	r.completedAt = &now
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (r *ReconciliationRun) ID() RunID                       { return r.id }
func (r *ReconciliationRun) TenantID() TenantID              { return r.tenantID }
func (r *ReconciliationRun) Type() ReconciliationType        { return r.reconcType }
func (r *ReconciliationRun) Status() RunStatus               { return r.status }
func (r *ReconciliationRun) PeriodFrom() time.Time           { return r.periodFrom }
func (r *ReconciliationRun) PeriodTo() time.Time             { return r.periodTo }
func (r *ReconciliationRun) TotalRecords() int               { return r.totalRecords }
func (r *ReconciliationRun) MatchedCount() int               { return r.matchedCount }
func (r *ReconciliationRun) DiscrepancyCount() int           { return r.discrepancyCount }
func (r *ReconciliationRun) ErrorMsg() string                { return r.errorMsg }
func (r *ReconciliationRun) CreatedAt() time.Time            { return r.createdAt }
func (r *ReconciliationRun) CompletedAt() *time.Time         { return r.completedAt }

func (r *ReconciliationRun) PullEvents() []Event {
	evs := r.events
	r.events = nil
	return evs
}

func (r *ReconciliationRun) record(e Event) { r.events = append(r.events, e) }
