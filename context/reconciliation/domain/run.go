package domain

import (
	"fmt"
	"time"
)

// RunStaleAfter es cuánto puede estar un run en running sin cerrarse antes de
// considerarlo abandonado (el proceso que lo tomó murió) y permitir tomarlo de
// nuevo. Holgado frente a lo que tarda un procesamiento normal.
const RunStaleAfter = 15 * time.Minute

// ReconciliationRun es el agregado raíz del contexto Reconciliation.
//
// Representa una ejecución del proceso de reconciliación para un período dado.
// La FSM es: pending → running → completed / failed (failed → running reprocesa)
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
	startedAt        *time.Time // último claim
	version          int64      // optimistic locking

	events []Event
}

// NewReconciliationRun crea un run en estado Pending.
func NewReconciliationRun(
	id RunID,
	tenantID TenantID,
	reconcType ReconciliationType,
	periodFrom, periodTo time.Time,
	now time.Time,
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
		createdAt:  now.UTC(),
	}, nil
}

// ReconstituteRun reconstruye desde el repositorio.
func ReconstituteRun(
	id RunID, tenantID TenantID,
	reconcType ReconciliationType, status RunStatus,
	periodFrom, periodTo time.Time,
	totalRecords, matchedCount, discrepancyCount int,
	errorMsg string, createdAt time.Time, completedAt *time.Time,
	startedAt *time.Time, version int64,
) *ReconciliationRun {
	return &ReconciliationRun{
		id: id, tenantID: tenantID,
		reconcType: reconcType, status: status,
		periodFrom: periodFrom, periodTo: periodTo,
		totalRecords: totalRecords, matchedCount: matchedCount,
		discrepancyCount: discrepancyCount,
		errorMsg: errorMsg, createdAt: createdAt, completedAt: completedAt,
		startedAt: startedAt, version: version,
	}
}

// Start marca el run como en ejecución. Un run fallido se puede reprocesar:
// el fallo no deja discrepancias guardadas, así que arranca de cero. Un run
// running se puede volver a tomar solo si está abandonado (RunStaleAfter
// desde el último claim, o sin registro de cuándo se tomó).
func (r *ReconciliationRun) Start(now time.Time) error {
	switch {
	case r.status == RunStatusPending, r.status == RunStatusFailed:
	case r.status == RunStatusRunning && r.IsStale(now):
	default:
		return fmt.Errorf("%w: current status is %q", ErrRunAlreadyRunning, r.status)
	}
	now = now.UTC()
	r.status = RunStatusRunning
	r.startedAt = &now
	r.errorMsg = ""
	r.completedAt = nil
	return nil
}

// IsStale indica si el run está running y abandonado a la hora now.
func (r *ReconciliationRun) IsStale(now time.Time) bool {
	return r.status == RunStatusRunning && (r.startedAt == nil || now.Sub(*r.startedAt) >= RunStaleAfter)
}

// Complete finaliza el run con los resultados del proceso. Solo desde running.
func (r *ReconciliationRun) Complete(totalRecords, matchedCount, discrepancyCount int, now time.Time) error {
	if r.status != RunStatusRunning {
		return fmt.Errorf("%w: current status is %q", ErrRunNotRunning, r.status)
	}
	now = now.UTC()
	r.status = RunStatusCompleted
	r.totalRecords = totalRecords
	r.matchedCount = matchedCount
	r.discrepancyCount = discrepancyCount
	r.completedAt = &now

	r.record(ReconciliationCompletedEvent{
		baseEvent:        newBase(r.tenantID.String(), now),
		RunID:            r.id.String(),
		TenantID:         r.tenantID.String(),
		Type:             r.reconcType.String(),
		TotalRecords:     totalRecords,
		MatchedCount:     matchedCount,
		DiscrepancyCount: discrepancyCount,
	})
	return nil
}

// Fail marca el run como fallido con el motivo del error.
// Solo desde running. Los contadores vuelven a cero: un run fallido no tiene
// resultados.
func (r *ReconciliationRun) Fail(reason string, now time.Time) error {
	if r.status != RunStatusRunning {
		return fmt.Errorf("%w: current status is %q", ErrRunNotRunning, r.status)
	}
	now = now.UTC()
	r.status = RunStatusFailed
	r.errorMsg = reason
	r.completedAt = &now
	r.totalRecords, r.matchedCount, r.discrepancyCount = 0, 0, 0

	r.record(ReconciliationFailedEvent{
		baseEvent: newBase(r.tenantID.String(), now),
		RunID:     r.id.String(),
		TenantID:  r.tenantID.String(),
		Type:      r.reconcType.String(),
		Error:     reason,
	})
	return nil
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
func (r *ReconciliationRun) StartedAt() *time.Time           { return r.startedAt }
func (r *ReconciliationRun) Version() int64                  { return r.version }

// MarkVersionPersisted la llama el repositorio tras un UPDATE exitoso.
func (r *ReconciliationRun) MarkVersionPersisted() { r.version++ }

func (r *ReconciliationRun) PullEvents() []Event {
	evs := r.events
	r.events = nil
	return evs
}

func (r *ReconciliationRun) record(e Event) { r.events = append(r.events, e) }
