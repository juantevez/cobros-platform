package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
	lifecycle
	paymentReader PaymentReader
	reportParser  ReportParser
}

func NewProcessReportUseCase(
	runRepo RunRepository,
	discrepancyRepo DiscrepancyRepository,
	paymentReader PaymentReader,
	reportParser ReportParser,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *ProcessReportUseCase {
	return &ProcessReportUseCase{
		lifecycle:     lifecycle{runs: runRepo, discs: discrepancyRepo, tx: txManager, pub: publisher, clock: clock},
		paymentReader: paymentReader,
		reportParser:  reportParser,
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

	run, err := uc.runs.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("find run: %w", err)
	}
	if !ownedBy(run, cmd.TenantID) {
		return domain.ErrRunNotFound
	}

	if err := uc.claim(ctx, run, domain.TypePayment); err != nil {
		return err
	}

	discrepancies, total, matched, err := uc.compare(ctx, run, pspRecords)
	if err != nil {
		return uc.fail(ctx, run, fmt.Errorf("compare: %w", err))
	}

	return uc.complete(ctx, run, discrepancies, total, matched)
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

	now := uc.clock.Now()
	newDisc := func(t domain.DiscrepancyType, ref, system, external string) {
		discrepancies = append(discrepancies, domain.NewDiscrepancy(
			domain.NewDiscrepancyID(), run.ID(), run.TenantID(), t, ref, system, external, now,
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

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ── ProcessInternalUseCase ────────────────────────────────────────────────────

// ProcessInternalUseCase verifica la coherencia del Ledger en el período del
// run: cada asiento balancea por moneda (el trigger de la base solo valida
// asientos con 2+ postings y suma todas las monedas juntas) y cada pago
// capturado tiene su asiento. total_records = asientos + capturas revisados.
type ProcessInternalUseCase struct {
	lifecycle
	ledgerChecker LedgerChecker
}

func NewProcessInternalUseCase(
	runRepo RunRepository,
	discrepancyRepo DiscrepancyRepository,
	ledgerChecker LedgerChecker,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *ProcessInternalUseCase {
	return &ProcessInternalUseCase{
		lifecycle:     lifecycle{runs: runRepo, discs: discrepancyRepo, tx: txManager, pub: publisher, clock: clock},
		ledgerChecker: ledgerChecker,
	}
}

func (uc *ProcessInternalUseCase) Execute(ctx context.Context, cmd ProcessInternalCmd) error {
	runID, err := domain.ParseRunID(cmd.RunID)
	if err != nil {
		return err
	}

	run, err := uc.runs.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("find run: %w", err)
	}
	if !ownedBy(run, cmd.TenantID) {
		return domain.ErrRunNotFound
	}

	if err := uc.claim(ctx, run, domain.TypeInternal); err != nil {
		return err
	}

	check, err := uc.ledgerChecker.Check(ctx, run.TenantID(), run.PeriodFrom(), run.PeriodTo())
	if err != nil {
		return uc.fail(ctx, run, fmt.Errorf("check ledger: %w", err))
	}

	discrepancies := ledgerDiscrepancies(run, check, uc.clock.Now())
	total := check.EntriesChecked + check.CapturesChecked
	return uc.complete(ctx, run, discrepancies, total, total-len(discrepancies))
}

// ledgerDiscrepancies arma una discrepancia por asiento desbalanceado (con el
// imbalance de cada moneda) y una por captura sin asiento.
func ledgerDiscrepancies(run *domain.ReconciliationRun, check LedgerCheck, now time.Time) []*domain.Discrepancy {
	var discrepancies []*domain.Discrepancy
	newDisc := func(t domain.DiscrepancyType, ref, system, external string) {
		discrepancies = append(discrepancies, domain.NewDiscrepancy(
			domain.NewDiscrepancyID(), run.ID(), run.TenantID(), t, ref, system, external, now,
		))
	}

	for i := 0; i < len(check.Unbalanced); {
		entryID := check.Unbalanced[i].EntryID
		var parts []string
		for ; i < len(check.Unbalanced) && check.Unbalanced[i].EntryID == entryID; i++ {
			if c := check.Unbalanced[i]; c.Currency != "" {
				parts = append(parts, fmt.Sprintf("%d %s", c.Imbalance, c.Currency))
			}
		}
		external := "sin postings"
		if len(parts) > 0 {
			external = strings.Join(parts, ", ") + " (créditos - débitos)"
		}
		newDisc(domain.DiscrepancyLedgerImbalance, entryID, "0", external)
	}
	for _, p := range check.MissingEntries {
		newDisc(domain.DiscrepancyMissingInLedger, p.PaymentID, toJSON(p), "")
	}
	return discrepancies
}
