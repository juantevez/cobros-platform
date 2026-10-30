package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// ProcessReportUseCase compara los pagos del sistema contra el informe del PSP
// y genera discrepancias para cada inconsistencia encontrada.
//
// Algoritmo de matching:
//  1. Parsear el CSV del PSP → []PSPRecord indexados por TransactionID (= psp_reference)
//  2. Cargar los pagos del sistema del período → []SystemPayment indexados por PSPReference
//  3. Comparar:
//     - En sistema, no en PSP → MissingInPSP
//     - En PSP, no en sistema → MissingInSystem
//     - En ambos, monto distinto → AmountMismatch
//     - En ambos, estado inconsistente → StatusMismatch
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

	run, err := uc.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("find run: %w", err)
	}
	if !ownedBy(run, cmd.TenantID) {
		return domain.ErrRunNotFound
	}

	if err := claim(ctx, uc.runRepo, run); err != nil {
		return err
	}

	discrepancies, total, matched, err := uc.compare(ctx, run, cmd.ReportData)
	if err != nil {
		return fail(ctx, uc.runRepo, run, fmt.Errorf("compare: %w", err))
	}

	return complete(ctx, uc.txManager, uc.runRepo, uc.discrepancyRepo, uc.publisher, run, discrepancies, total, matched)
}

func (uc *ProcessReportUseCase) compare(
	ctx context.Context,
	run *domain.ReconciliationRun,
	reportData []byte,
) (discrepancies []*domain.Discrepancy, total, matched int, err error) {

	// 1. Parsear el informe del PSP.
	pspRecords, err := uc.reportParser.Parse(reportData)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("parse report: %w", err)
	}

	// 2. Cargar pagos del sistema para el período.
	systemPayments, err := uc.paymentReader.ReadByPeriod(
		ctx, run.TenantID(), run.PeriodFrom(), run.PeriodTo(),
	)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read payments: %w", err)
	}

	// 3. Indexar por psp_reference / transaction_id para O(1) lookup.
	pspIndex := make(map[string]PSPRecord, len(pspRecords))
	for _, r := range pspRecords {
		pspIndex[r.TransactionID] = r
	}

	systemIndex := make(map[string]SystemPayment, len(systemPayments))
	for _, p := range systemPayments {
		if p.PSPReference != "" {
			systemIndex[p.PSPReference] = p
		}
	}

	total = max(len(pspRecords), len(systemPayments))

	// 4. Recorrer sistema → buscar en PSP.
	for ref, sysPayment := range systemIndex {
		pspRec, found := pspIndex[ref]
		if !found {
			// El sistema tiene el pago pero el PSP no lo reporta.
			discrepancies = append(discrepancies, domain.NewDiscrepancy(
				domain.NewDiscrepancyID(), run.ID(), run.TenantID(),
				domain.DiscrepancyMissingInPSP,
				ref,
				toJSON(sysPayment),
				"",
			))
			continue
		}

		// Comparar monto.
		if sysPayment.Amount != pspRec.Amount {
			discrepancies = append(discrepancies, domain.NewDiscrepancy(
				domain.NewDiscrepancyID(), run.ID(), run.TenantID(),
				domain.DiscrepancyAmountMismatch,
				ref,
				fmt.Sprintf("%d %s", sysPayment.Amount, sysPayment.Currency),
				fmt.Sprintf("%d %s", pspRec.Amount, pspRec.Currency),
			))
			continue
		}

		// Comparar estado.
		if isStatusMismatch(sysPayment.Status, pspRec.Status) {
			discrepancies = append(discrepancies, domain.NewDiscrepancy(
				domain.NewDiscrepancyID(), run.ID(), run.TenantID(),
				domain.DiscrepancyStatusMismatch,
				ref,
				sysPayment.Status,
				pspRec.Status,
			))
			continue
		}

		matched++
	}

	// 5. Recorrer PSP → buscar los que no existen en sistema.
	for ref, pspRec := range pspIndex {
		if _, found := systemIndex[ref]; !found {
			discrepancies = append(discrepancies, domain.NewDiscrepancy(
				domain.NewDiscrepancyID(), run.ID(), run.TenantID(),
				domain.DiscrepancyMissingInSystem,
				ref,
				"",
				toJSON(pspRec),
			))
		}
	}

	return discrepancies, total, matched, nil
}

// isStatusMismatch detecta inconsistencias críticas de estado.
// No todos los estados distintos son errores: ej. "refunded" en PSP puede
// coincidir con "captured" en sistema si el reembolso aún no fue procesado.
func isStatusMismatch(systemStatus, pspStatus string) bool {
	criticalMismatches := map[[2]string]bool{
		{"captured", "rejected"}: true,
		{"captured", "failed"}:   true,
		{"failed", "captured"}:   true,
	}
	return criticalMismatches[[2]string{systemStatus, pspStatus}]
}

// ownedBy indica si el run pertenece al tenant del caller.
// Aislamiento: un tenant nunca accede a runs de otro tenant ni a los globales;
// un caller sin tenant (proceso interno) sólo accede a los globales.
func ownedBy(run *domain.ReconciliationRun, tenantID string) bool {
	return run.TenantID().String() == tenantID
}

// claim pasa el run a running con un UPDATE condicionado al estado leído: de
// dos procesos concurrentes sobre el mismo run, solo uno lo toma (el otro
// recibe ErrRunAlreadyRunning).
func claim(ctx context.Context, runs RunRepository, run *domain.ReconciliationRun) error {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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

	if err := claim(ctx, uc.runRepo, run); err != nil {
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
