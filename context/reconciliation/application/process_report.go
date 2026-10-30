package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// ProcessReportUseCase compara los pagos del sistema contra el informe del PSP
// y genera discrepancias para cada inconsistencia encontrada.
//
// Algoritmo de matching (por referencia: transaction_id = psp_reference):
//   - Referencia repetida en el informe o en el sistema → DuplicateReference
//   - En sistema, no en PSP → MissingInPSP
//   - En PSP, no en sistema → MissingInSystem, salvo que el informe la ubique
//     fuera del período (processed_at): esa queda para el run que corresponda
//   - En ambos, monto o moneda distintos → AmountMismatch
//   - En ambos, estados que no se corresponden → StatusMismatch
//
// total_records = referencias evaluadas = matched + discrepancias.
type ProcessReportUseCase struct {
	runRepo         RunRepository
	discrepancyRepo DiscrepancyRepository
	paymentReader   PaymentReader
	reportParser    ReportParser
	txManager       TxManager
	publisher       EventPublisher
}

func NewProcessReportUseCase(
	runRepo RunRepository,
	discrepancyRepo DiscrepancyRepository,
	paymentReader PaymentReader,
	reportParser ReportParser,
	txManager TxManager,
	publisher EventPublisher,
) *ProcessReportUseCase {
	return &ProcessReportUseCase{
		runRepo:         runRepo,
		discrepancyRepo: discrepancyRepo,
		paymentReader:   paymentReader,
		reportParser:    reportParser,
		txManager:       txManager,
		publisher:       publisher,
	}
}

func (uc *ProcessReportUseCase) Execute(ctx context.Context, cmd ProcessReportCmd) error {
	if len(cmd.ReportData) == 0 {
		return domain.ErrEmptyReport
	}

	runID, err := domain.ParseRunID(cmd.RunID)
	if err != nil {
		return err
	}

	// Parsear antes de tomar el run: un informe mal formado es un error del
	// cliente y no debe dejar el run failed.
	pspRecords, err := uc.reportParser.Parse(cmd.ReportData)
	if err != nil {
		return fmt.Errorf("parse report: %w", err)
	}

	run, err := uc.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("find run: %w", err)
	}
	if !ownedBy(run, cmd.TenantID) {
		return domain.ErrRunNotFound
	}

	if err := claim(ctx, uc.runRepo, run, domain.TypePayment); err != nil {
		return err
	}

	discrepancies, total, matched, err := uc.compare(ctx, run, pspRecords)
	if err != nil {
		return fail(ctx, uc.runRepo, run, fmt.Errorf("compare: %w", err))
	}

	return complete(ctx, uc.txManager, uc.runRepo, uc.discrepancyRepo, uc.publisher, run, discrepancies, total, matched)
}

func (uc *ProcessReportUseCase) compare(
	ctx context.Context,
	run *domain.ReconciliationRun,
	pspRecords []PSPRecord,
) (discrepancies []*domain.Discrepancy, total, matched int, err error) {

	systemPayments, err := uc.paymentReader.ReadByPeriod(
		ctx, run.TenantID(), run.PeriodFrom(), run.PeriodTo(),
	)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read payments: %w", err)
	}

	// Indexar ambos lados por referencia. Los pagos sin psp_reference no
	// llegaron al PSP: no hay contra qué compararlos.
	pspIndex := make(map[string][]PSPRecord, len(pspRecords))
	for _, r := range pspRecords {
		pspIndex[r.TransactionID] = append(pspIndex[r.TransactionID], r)
	}
	systemIndex := make(map[string][]SystemPayment, len(systemPayments))
	for _, p := range systemPayments {
		if p.PSPReference != "" {
			systemIndex[p.PSPReference] = append(systemIndex[p.PSPReference], p)
		}
	}

	refs := make([]string, 0, len(pspIndex)+len(systemIndex))
	for ref := range systemIndex {
		refs = append(refs, ref)
	}
	for ref := range pspIndex {
		if _, ok := systemIndex[ref]; !ok {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs) // orden estable de las discrepancias

	newDisc := func(t domain.DiscrepancyType, ref, system, external string) {
		discrepancies = append(discrepancies, domain.NewDiscrepancy(
			domain.NewDiscrepancyID(), run.ID(), run.TenantID(), t, ref, system, external,
		))
	}

	for _, ref := range refs {
		sys, psp := systemIndex[ref], pspIndex[ref]

		if len(sys) == 0 && !inPeriod(psp[0], run) && len(psp) == 1 {
			continue // procesado por el PSP en otro período
		}
		total++

		switch {
		case len(sys) > 1 || len(psp) > 1:
			newDisc(domain.DiscrepancyDuplicateReference, ref, jsonOrEmpty(sys), jsonOrEmpty(psp))
		case len(psp) == 0:
			newDisc(domain.DiscrepancyMissingInPSP, ref, toJSON(sys[0]), "")
		case len(sys) == 0:
			newDisc(domain.DiscrepancyMissingInSystem, ref, "", toJSON(psp[0]))
		case sys[0].Amount != psp[0].Amount || !strings.EqualFold(sys[0].Currency, psp[0].Currency):
			newDisc(domain.DiscrepancyAmountMismatch, ref,
				fmt.Sprintf("%d %s", sys[0].Amount, sys[0].Currency),
				fmt.Sprintf("%d %s", psp[0].Amount, psp[0].Currency))
		case isStatusMismatch(sys[0].Status, psp[0].Status):
			newDisc(domain.DiscrepancyStatusMismatch, ref, sys[0].Status, psp[0].Status)
		default:
			matched++
		}
	}

	return discrepancies, total, matched, nil
}

// inPeriod indica si el PSP procesó el registro dentro del período del run.
// Sin processed_at no se puede saber: se asume que sí.
func inPeriod(r PSPRecord, run *domain.ReconciliationRun) bool {
	if r.ProcessedAt == nil {
		return true
	}
	return !r.ProcessedAt.Before(run.PeriodFrom()) && r.ProcessedAt.Before(run.PeriodTo())
}

// Desenlace de un pago en cada lado. Los estados de ambos se traducen a estos
// valores y se comparan; un estado del PSP desconocido nunca coincide.
const (
	outcomePending  = "pending"
	outcomeCaptured = "captured"
	outcomeFailed   = "failed"
	outcomeRefunded = "refunded"
)

var systemOutcome = map[string]string{
	"initiated":     outcomePending,
	"processing":    outcomePending,
	"captured":      outcomeCaptured,
	"risk_rejected": outcomeFailed,
	"failed":        outcomeFailed,
	"refunded":      outcomeRefunded,
}

var pspOutcome = map[string]string{
	"pending":  outcomePending,
	"captured": outcomeCaptured,
	"rejected": outcomeFailed,
	"failed":   outcomeFailed,
	"refunded": outcomeRefunded,
}

// isStatusMismatch indica si los estados de ambos lados no describen el mismo
// desenlace. Incluye los reembolsos: si uno de los lados lo registra y el otro
// no, el dinero devuelto no coincide (el operador decide si es un desfase).
func isStatusMismatch(systemStatus, pspStatus string) bool {
	sys, okSys := systemOutcome[systemStatus]
	psp, okPSP := pspOutcome[pspStatus]
	return !okSys || !okPSP || sys != psp
}

// jsonOrEmpty serializa los registros de un lado, o "" si no hay ninguno.
func jsonOrEmpty[T any](records []T) string {
	if len(records) == 0 {
		return ""
	}
	return toJSON(records)
}

// ownedBy indica si el run pertenece al tenant del caller.
// Aislamiento: un tenant nunca accede a runs de otro tenant ni a los globales;
// un caller sin tenant (proceso interno) sólo accede a los globales.
func ownedBy(run *domain.ReconciliationRun, tenantID string) bool {
	return run.TenantID().String() == tenantID
}

// claim verifica que la operación corresponda al tipo del run y lo pasa a
// running con un UPDATE condicionado al estado leído: de dos procesos
// concurrentes sobre el mismo run, solo uno lo toma (el otro recibe
// ErrRunAlreadyRunning).
func claim(ctx context.Context, runs RunRepository, run *domain.ReconciliationRun, want domain.ReconciliationType) error {
	if run.Type() != want {
		return fmt.Errorf("%w: run %s is %q, operation requires %q", domain.ErrRunTypeMismatch, run.ID(), run.Type(), want)
	}
	from := run.Status()
	if err := run.Start(); err != nil {
		return err
	}
	if err := runs.Update(ctx, run, from); err != nil {
		return fmt.Errorf("claim run: %w", err)
	}
	return nil
}

// complete guarda las discrepancias, cierra el run y publica el evento en una
// sola transacción. Si falla, no queda nada a medias y el run pasa a failed.
func complete(
	ctx context.Context,
	tx TxManager,
	runs RunRepository,
	discs DiscrepancyRepository,
	pub EventPublisher,
	run *domain.ReconciliationRun,
	discrepancies []*domain.Discrepancy,
	total, matched int,
) error {
	err := tx.RunInTx(ctx, func(ctx context.Context) error {
		if len(discrepancies) > 0 {
			if err := discs.SaveAll(ctx, discrepancies); err != nil {
				return fmt.Errorf("save discrepancies: %w", err)
			}
		}
		run.Complete(total, matched, len(discrepancies))
		if err := runs.Update(ctx, run, domain.RunStatusRunning); err != nil {
			return fmt.Errorf("update run completed: %w", err)
		}
		return pub.Publish(ctx, run.PullEvents()...)
	})
	if err != nil {
		return fail(ctx, runs, run, err)
	}
	return nil
}

// fail marca el run como fallido y devuelve la causa. Si tampoco se puede
// guardar el fallo, el run queda running y el error lo informa.
func fail(ctx context.Context, runs RunRepository, run *domain.ReconciliationRun, cause error) error {
	run.Fail(cause.Error())
	if err := runs.Update(ctx, run, domain.RunStatusRunning); err != nil {
		return fmt.Errorf("%w (mark run failed: %v)", cause, err)
	}
	return cause
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ── ProcessInternalUseCase ────────────────────────────────────────────────────

// ProcessInternalUseCase verifica la coherencia matemática del Ledger.
// Verifica que sum(débitos) == sum(créditos) para el período del run.
type ProcessInternalUseCase struct {
	runRepo         RunRepository
	discrepancyRepo DiscrepancyRepository
	ledgerChecker   LedgerChecker
	txManager       TxManager
	publisher       EventPublisher
}

func NewProcessInternalUseCase(
	runRepo RunRepository,
	discrepancyRepo DiscrepancyRepository,
	ledgerChecker LedgerChecker,
	txManager TxManager,
	publisher EventPublisher,
) *ProcessInternalUseCase {
	return &ProcessInternalUseCase{
		runRepo: runRepo, discrepancyRepo: discrepancyRepo,
		ledgerChecker: ledgerChecker, txManager: txManager, publisher: publisher,
	}
}

func (uc *ProcessInternalUseCase) Execute(ctx context.Context, cmd ProcessInternalCmd) error {
	runID, err := domain.ParseRunID(cmd.RunID)
	if err != nil {
		return err
	}

	run, err := uc.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("find run: %w", err)
	}
	if !ownedBy(run, cmd.TenantID) {
		return domain.ErrRunNotFound
	}

	if err := claim(ctx, uc.runRepo, run, domain.TypeInternal); err != nil {
		return err
	}

	// Verificar balance del Ledger para el período.
	imbalance, err := uc.ledgerChecker.CheckBalance(ctx, run.TenantID(), run.PeriodFrom(), run.PeriodTo())
	if err != nil {
		return fail(ctx, uc.runRepo, run, fmt.Errorf("check ledger balance: %w", err))
	}

	var discrepancies []*domain.Discrepancy
	matched := 1
	if imbalance != 0 {
		matched = 0
		discrepancies = append(discrepancies, domain.NewDiscrepancy(
			domain.NewDiscrepancyID(), run.ID(), run.TenantID(),
			domain.DiscrepancyLedgerImbalance,
			"global",
			"0",
			fmt.Sprintf("%d (centavos fuera de balance)", imbalance),
		))
	}

	return complete(ctx, uc.txManager, uc.runRepo, uc.discrepancyRepo, uc.publisher, run, discrepancies, 1, matched)
}
