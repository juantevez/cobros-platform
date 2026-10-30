package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

var testReport = []byte("transaction_id,amount,currency,status\n")

type reportDeps struct {
	run    *domain.ReconciliationRun
	runs   *fakeRunRepo
	discs  *fakeDiscRepo
	reader *fakePaymentReader
	parser *fakeParser
	tx     *fakeTx
	pub    *fakePublisher
}

func newReportDeps() *reportDeps {
	run := pendingRun(tenantA, domain.TypePayment)
	return &reportDeps{
		run:    run,
		runs:   newRunRepo(run),
		discs:  newDiscRepo(),
		reader: &fakePaymentReader{},
		parser: &fakeParser{},
		tx:     &fakeTx{},
		pub:    &fakePublisher{},
	}
}

func (d *reportDeps) exec() error {
	uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.tx, d.pub, clock)
	return uc.Execute(context.Background(), ProcessReportCmd{TenantID: d.run.TenantID().String(), RunID: d.run.ID().String(), ReportData: testReport})
}

func sysPay(ref string, amount int64, status string) SystemPayment {
	return SystemPayment{PaymentID: "pay-" + ref, PSPReference: ref, Amount: amount, Currency: "ARS", Status: status}
}

func pspRec(ref string, amount int64, status string) PSPRecord {
	return PSPRecord{TransactionID: ref, Amount: amount, Currency: "ARS", Status: status}
}

func TestProcessReportUseCase_Execute(t *testing.T) {
	t.Run("all records match completes run without discrepancies", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured"), sysPay("B", 200, "captured")}
		d.parser.records = []PSPRecord{pspRec("A", 100, "captured"), pspRec("B", 200, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted {
			t.Errorf("status = %q, want completed", d.run.Status())
		}
		if d.run.TotalRecords() != 2 || d.run.MatchedCount() != 2 || d.run.DiscrepancyCount() != 0 {
			t.Errorf("counts = %d/%d/%d", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
		}
		if d.discs.saveCalls != 0 {
			t.Error("SaveAll should not be called without discrepancies")
		}
		wantUpdates := []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusCompleted}
		if !reflect.DeepEqual(d.runs.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.runs.updates, wantUpdates)
		}
		wantFroms := []domain.RunStatus{domain.RunStatusPending, domain.RunStatusRunning}
		if !reflect.DeepEqual(d.runs.froms, wantFroms) {
			t.Errorf("updates conditioned on %v, want %v", d.runs.froms, wantFroms)
		}
		if d.tx.calls != 1 {
			t.Errorf("tx calls = %d, want 1", d.tx.calls)
		}
		if len(d.pub.events) != 1 || d.pub.events[0].EventType() != "reconciliation.run.completed.v1" {
			t.Errorf("events = %v", d.pub.events)
		}
	})

	t.Run("dependencies receive run data", func(t *testing.T) {
		d := newReportDeps()

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(d.parser.lastData) != string(testReport) {
			t.Errorf("parser got %q", d.parser.lastData)
		}
		if d.reader.lastTenant != domain.TenantID(tenantA) || !d.reader.lastFrom.Equal(periodFrom) || !d.reader.lastTo.Equal(periodTo) {
			t.Errorf("reader got tenant=%q from=%v to=%v", d.reader.lastTenant, d.reader.lastFrom, d.reader.lastTo)
		}
	})

	t.Run("classifies every kind of discrepancy", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{
			sysPay("OK", 100, "captured"),
			sysPay("ONLY-SYS", 100, "captured"),
			sysPay("AMOUNT", 100, "captured"),
			sysPay("STATUS", 100, "captured"),
		}
		d.parser.records = []PSPRecord{
			pspRec("OK", 100, "captured"),
			pspRec("ONLY-PSP", 50, "captured"),
			pspRec("AMOUNT", 90, "captured"),
			pspRec("STATUS", 100, "rejected"),
		}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.TotalRecords() != 5 || d.run.MatchedCount() != 1 || d.run.DiscrepancyCount() != 4 {
			t.Errorf("counts = %d/%d/%d, want 5/1/4", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
		}
		if d.discs.saveCalls != 1 {
			t.Errorf("SaveAll calls = %d, want 1", d.discs.saveCalls)
		}

		got := d.discs.byRecord()
		want := map[string]struct {
			typ           domain.DiscrepancyType
			system, extnl string
		}{
			"AMOUNT": {domain.DiscrepancyAmountMismatch, "100 ARS", "90 ARS"},
			"STATUS": {domain.DiscrepancyStatusMismatch, "captured", "rejected"},
		}
		for ref, w := range want {
			disc := got[ref]
			if disc == nil {
				t.Fatalf("missing discrepancy for %s", ref)
			}
			if disc.Type() != w.typ || disc.SystemValue() != w.system || disc.ExternalValue() != w.extnl {
				t.Errorf("%s: type=%q system=%q external=%q", ref, disc.Type(), disc.SystemValue(), disc.ExternalValue())
			}
		}

		onlySys := got["ONLY-SYS"]
		if onlySys == nil || onlySys.Type() != domain.DiscrepancyMissingInPSP || onlySys.ExternalValue() != "" ||
			!strings.Contains(onlySys.SystemValue(), `"PaymentID":"pay-ONLY-SYS"`) {
			t.Errorf("missing_in_psp discrepancy = %+v", onlySys)
		}
		onlyPSP := got["ONLY-PSP"]
		if onlyPSP == nil || onlyPSP.Type() != domain.DiscrepancyMissingInSystem || onlyPSP.SystemValue() != "" ||
			!strings.Contains(onlyPSP.ExternalValue(), `"TransactionID":"ONLY-PSP"`) {
			t.Errorf("missing_in_system discrepancy = %+v", onlyPSP)
		}

		for _, disc := range d.discs.saved {
			if disc.RunID() != d.run.ID() || disc.TenantID() != domain.TenantID(tenantA) || disc.Status() != domain.DiscrepancyOpen {
				t.Errorf("discrepancy %s not linked to run/tenant or not open", disc.RecordID())
			}
		}
	})

	t.Run("amount mismatch takes precedence over status mismatch", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("X", 100, "captured")}
		d.parser.records = []PSPRecord{pspRec("X", 90, "rejected")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.discs.saved) != 1 || d.discs.saved[0].Type() != domain.DiscrepancyAmountMismatch {
			t.Fatalf("expected single amount_mismatch, got %v", d.discs.saved)
		}
	})

	statusCases := []struct {
		system, psp string
		mismatch    bool
	}{
		{"captured", "captured", false},
		{"failed", "rejected", false},
		{"failed", "failed", false},
		{"refunded", "refunded", false},
		{"processing", "pending", false},
		{"initiated", "pending", false},
		{"captured", "rejected", true},
		{"captured", "failed", true},
		{"failed", "captured", true},
		{"processing", "captured", true}, // el sistema quedó trabado
		{"captured", "pending", true},
		{"captured", "refunded", true},   // el sistema no registró el reembolso
		{"refunded", "captured", true},   // el PSP no registró el reembolso
		{"captured", "chargeback", true}, // estado del PSP desconocido
		{"unknown", "captured", true},
	}
	for _, tc := range statusCases {
		t.Run("status "+tc.system+" vs "+tc.psp, func(t *testing.T) {
			d := newReportDeps()
			d.reader.payments = []SystemPayment{sysPay("X", 100, tc.system)}
			d.parser.records = []PSPRecord{pspRec("X", 100, tc.psp)}

			if err := d.exec(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotMismatch := len(d.discs.saved) == 1 && d.discs.saved[0].Type() == domain.DiscrepancyStatusMismatch
			if gotMismatch != tc.mismatch {
				t.Errorf("mismatch = %v, want %v", gotMismatch, tc.mismatch)
			}
			if !tc.mismatch && d.run.MatchedCount() != 1 {
				t.Errorf("matched = %d, want 1", d.run.MatchedCount())
			}
		})
	}

	t.Run("same amount in another currency is an amount mismatch", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("X", 100, "captured")}
		usd := pspRec("X", 100, "captured")
		usd.Currency = "USD"
		d.parser.records = []PSPRecord{usd}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.discs.saved) != 1 || d.discs.saved[0].Type() != domain.DiscrepancyAmountMismatch ||
			d.discs.saved[0].SystemValue() != "100 ARS" || d.discs.saved[0].ExternalValue() != "100 USD" {
			t.Fatalf("expected amount_mismatch 100 ARS vs 100 USD, got %v", d.discs.saved)
		}
	})

	t.Run("currency comparison ignores case", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("X", 100, "captured")}
		lower := pspRec("X", 100, "captured")
		lower.Currency = "ars"
		d.parser.records = []PSPRecord{lower}

		if err := d.exec(); err != nil || d.run.MatchedCount() != 1 {
			t.Fatalf("expected match, err=%v matched=%d", err, d.run.MatchedCount())
		}
	})

	t.Run("duplicated references are reported once and not matched", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("DUP-PSP", 100, "captured"), sysPay("DUP-SYS", 100, "captured"), sysPay("DUP-SYS", 100, "captured")}
		d.parser.records = []PSPRecord{pspRec("DUP-PSP", 100, "captured"), pspRec("DUP-PSP", 100, "refunded"), pspRec("DUP-SYS", 100, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := d.discs.byRecord()
		if len(d.discs.saved) != 2 || d.run.MatchedCount() != 0 || d.run.TotalRecords() != 2 {
			t.Fatalf("discrepancies=%d matched=%d total=%d, want 2/0/2", len(d.discs.saved), d.run.MatchedCount(), d.run.TotalRecords())
		}
		for _, ref := range []string{"DUP-PSP", "DUP-SYS"} {
			disc := got[ref]
			if disc == nil || disc.Type() != domain.DiscrepancyDuplicateReference ||
				!strings.HasPrefix(disc.SystemValue(), "[") || !strings.HasPrefix(disc.ExternalValue(), "[") {
				t.Errorf("%s: %+v", ref, disc)
			}
		}
		if strings.Count(got["DUP-PSP"].ExternalValue(), "TransactionID") != 2 {
			t.Errorf("both PSP rows should be kept: %s", got["DUP-PSP"].ExternalValue())
		}
	})

	t.Run("duplicated reference only in the report has empty system value", func(t *testing.T) {
		d := newReportDeps()
		d.parser.records = []PSPRecord{pspRec("D", 1, "captured"), pspRec("D", 1, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.discs.saved) != 1 || d.discs.saved[0].Type() != domain.DiscrepancyDuplicateReference || d.discs.saved[0].SystemValue() != "" {
			t.Fatalf("got %v", d.discs.saved)
		}
	})

	t.Run("report rows processed outside the period are left for their own run", func(t *testing.T) {
		d := newReportDeps()
		before, inside, after := periodFrom.Add(-time.Second), periodFrom, periodTo
		at := func(r PSPRecord, ts time.Time) PSPRecord { r.ProcessedAt = &ts; return r }
		d.reader.payments = []SystemPayment{sysPay("LATE-MATCH", 100, "captured")}
		d.parser.records = []PSPRecord{
			at(pspRec("EARLY", 1, "captured"), before),       // fuera, sin pago: se ignora
			at(pspRec("LATE", 1, "captured"), after),         // period_to es exclusivo: se ignora
			at(pspRec("INSIDE", 1, "captured"), inside),      // dentro: falta en el sistema
			at(pspRec("LATE-MATCH", 100, "captured"), after), // fuera, pero el pago es del período: se compara
			pspRec("NO-DATE", 1, "captured"),                 // sin fecha: se asume dentro
		}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := d.discs.byRecord()
		if len(got) != 2 || got["INSIDE"] == nil || got["NO-DATE"] == nil {
			t.Errorf("discrepancies = %v, want INSIDE and NO-DATE", got)
		}
		if d.run.TotalRecords() != 3 || d.run.MatchedCount() != 1 {
			t.Errorf("total=%d matched=%d, want 3/1", d.run.TotalRecords(), d.run.MatchedCount())
		}
	})

	t.Run("discrepancies are saved in reference order", func(t *testing.T) {
		d := newReportDeps()
		d.parser.records = []PSPRecord{pspRec("C", 1, "captured"), pspRec("A", 1, "captured"), pspRec("B", 1, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var order []string
		for _, disc := range d.discs.saved {
			order = append(order, disc.RecordID())
		}
		if strings.Join(order, ",") != "A,B,C" {
			t.Errorf("order = %v", order)
		}
	})

	t.Run("system payments without psp reference are not evaluated", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured"), sysPay("", 100, "failed"), sysPay("", 50, "failed")}
		d.parser.records = []PSPRecord{pspRec("A", 100, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.discs.saved) != 0 {
			t.Errorf("payments without reference should not create discrepancies, got %d", len(d.discs.saved))
		}
		if d.run.TotalRecords() != 1 || d.run.MatchedCount() != 1 {
			t.Errorf("total=%d matched=%d, want 1/1", d.run.TotalRecords(), d.run.MatchedCount())
		}
	})

	t.Run("total is every reference evaluated", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured")}
		d.parser.records = []PSPRecord{pspRec("A", 100, "captured"), pspRec("B", 1, "captured"), pspRec("C", 1, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.TotalRecords() != 3 || d.run.DiscrepancyCount() != 2 {
			t.Errorf("total=%d discrepancies=%d, want 3/2", d.run.TotalRecords(), d.run.DiscrepancyCount())
		}
	})

	t.Run("run of another tenant is reported as not found", func(t *testing.T) {
		d := newReportDeps()
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.tx, d.pub, clock)

		err := uc.Execute(context.Background(), ProcessReportCmd{TenantID: tenantB, RunID: d.run.ID().String(), ReportData: testReport})
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
		if d.run.Status() != domain.RunStatusPending || len(d.runs.updates) != 0 || d.reader.calls != 0 {
			t.Error("foreign run must not be touched")
		}
	})

	t.Run("global run is not visible to a tenant", func(t *testing.T) {
		d := newReportDeps()
		d.run = pendingRun("", domain.TypePayment)
		d.runs = newRunRepo(d.run)
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.tx, d.pub, clock)

		err := uc.Execute(context.Background(), ProcessReportCmd{TenantID: tenantA, RunID: d.run.ID().String(), ReportData: testReport})
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
	})

	t.Run("global run is processed by caller without tenant", func(t *testing.T) {
		d := newReportDeps()
		d.run = pendingRun("", domain.TypePayment)
		d.runs = newRunRepo(d.run)

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted {
			t.Errorf("status = %q, want completed", d.run.Status())
		}
	})

	t.Run("empty report rejected before touching repo", func(t *testing.T) {
		d := newReportDeps()
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.tx, d.pub, clock)

		err := uc.Execute(context.Background(), ProcessReportCmd{RunID: "not-even-a-uuid"})
		if !errors.Is(err, domain.ErrEmptyReport) {
			t.Fatalf("expected ErrEmptyReport, got %v", err)
		}
		if len(d.runs.updates) != 0 {
			t.Error("run should not be touched")
		}
	})

	t.Run("invalid run id", func(t *testing.T) {
		d := newReportDeps()
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.tx, d.pub, clock)

		err := uc.Execute(context.Background(), ProcessReportCmd{RunID: "x", ReportData: testReport})
		if err == nil || !strings.Contains(err.Error(), "invalid run id") {
			t.Fatalf("expected invalid run id error, got %v", err)
		}
	})

	t.Run("unknown run is wrapped ErrRunNotFound", func(t *testing.T) {
		d := newReportDeps()
		d.runs = newRunRepo()

		err := d.exec()
		if !errors.Is(err, domain.ErrRunNotFound) || !strings.Contains(err.Error(), "find run") {
			t.Fatalf("expected wrapped ErrRunNotFound, got %v", err)
		}
	})

	for _, st := range []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusCompleted} {
		t.Run("run in status "+st.String()+" is rejected", func(t *testing.T) {
			d := newReportDeps()
			d.run = runWithStatus(st)
			d.runs = newRunRepo(d.run)

			if err := d.exec(); !errors.Is(err, domain.ErrRunAlreadyRunning) {
				t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
			}
			if len(d.runs.updates) != 0 || d.reader.calls != 0 {
				t.Error("nothing should be processed")
			}
		})
	}

	t.Run("internal_ledger run is rejected without touching it", func(t *testing.T) {
		d := newReportDeps()
		d.run = pendingRun(tenantA, domain.TypeInternal)
		d.runs = newRunRepo(d.run)

		err := d.exec()
		if !errors.Is(err, domain.ErrRunTypeMismatch) {
			t.Fatalf("expected ErrRunTypeMismatch, got %v", err)
		}
		if d.run.Status() != domain.RunStatusPending || len(d.runs.updates) != 0 || d.reader.calls != 0 {
			t.Error("run of another type must not be processed")
		}
	})

	t.Run("failed run is reprocessed", func(t *testing.T) {
		d := newReportDeps()
		d.run = runWithStatus(domain.RunStatusFailed)
		d.runs = newRunRepo(d.run)

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted || d.run.ErrorMsg() != "" {
			t.Errorf("status=%q errorMsg=%q", d.run.Status(), d.run.ErrorMsg())
		}
		if d.runs.froms[0] != domain.RunStatusFailed {
			t.Errorf("claim conditioned on %q, want failed", d.runs.froms[0])
		}
	})

	t.Run("claim lost to a concurrent process aborts", func(t *testing.T) {
		d := newReportDeps()
		d.runs.updateErr = domain.ErrRunAlreadyRunning
		d.runs.updateErrAt = 1

		err := d.exec()
		if !errors.Is(err, domain.ErrRunAlreadyRunning) || !strings.Contains(err.Error(), "claim run") {
			t.Fatalf("expected wrapped ErrRunAlreadyRunning, got %v", err)
		}
		if d.reader.calls != 0 || d.tx.calls != 0 || len(d.runs.updates) != 1 {
			t.Error("the loser must not process nor touch the run again")
		}
	})

	t.Run("parse error leaves the run untouched", func(t *testing.T) {
		d := newReportDeps()
		d.parser.err = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "parse report") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.run.Status() != domain.RunStatusPending || len(d.runs.updates) != 0 || d.reader.calls != 0 {
			t.Error("a malformed report must not claim nor fail the run")
		}
	})

	failures := []struct {
		name    string
		setup   func(*reportDeps)
		wantMsg string
	}{
		{"reader error", func(d *reportDeps) { d.reader.err = errBoom }, "compare: read payments"},
		{"save discrepancies error", func(d *reportDeps) {
			d.reader.payments = []SystemPayment{sysPay("A", 100, "captured")}
			d.discs.saveErr = errBoom
		}, "save discrepancies"},
	}
	for _, tc := range failures {
		t.Run(tc.name+" marks run failed", func(t *testing.T) {
			d := newReportDeps()
			tc.setup(d)

			err := d.exec()
			if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
			if d.run.Status() != domain.RunStatusFailed || !strings.Contains(d.run.ErrorMsg(), "boom") {
				t.Errorf("status=%q errorMsg=%q", d.run.Status(), d.run.ErrorMsg())
			}
			wantUpdates := []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusFailed}
			if !reflect.DeepEqual(d.runs.updates, wantUpdates) {
				t.Errorf("updates = %v, want %v", d.runs.updates, wantUpdates)
			}
			assertOnlyFailedEvent(t, d.pub, d.run)
			if d.run.TotalRecords() != 0 || d.run.DiscrepancyCount() != 0 {
				t.Error("failed run must not keep results")
			}
		})
	}

	t.Run("save discrepancies error rolls back", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured")}
		d.discs.saveErr = errBoom

		_ = d.exec()
		if d.tx.rollbacks != 1 {
			t.Errorf("rollbacks = %d, want 1", d.tx.rollbacks)
		}
	})

	t.Run("update completed error rolls back and marks run failed", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured")}
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 2

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update run completed") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.tx.rollbacks != 1 {
			t.Errorf("rollbacks = %d, want 1", d.tx.rollbacks)
		}
		assertOnlyFailedEvent(t, d.pub, d.run)
		wantUpdates := []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusCompleted, domain.RunStatusFailed}
		if !reflect.DeepEqual(d.runs.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.runs.updates, wantUpdates)
		}
		if d.run.Status() != domain.RunStatusFailed || d.run.DiscrepancyCount() != 0 {
			t.Errorf("status=%q discrepancies=%d", d.run.Status(), d.run.DiscrepancyCount())
		}
	})

	t.Run("publish error rolls back both the completion and the failure", func(t *testing.T) {
		d := newReportDeps()
		d.pub.err = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "mark run failed") {
			t.Fatalf("expected errBoom plus mark failed error, got %v", err)
		}
		if d.tx.rollbacks != 2 {
			t.Errorf("rollbacks = %d, want 2", d.tx.rollbacks)
		}
	})

	t.Run("tx begin error leaves the run running and reports it", func(t *testing.T) {
		d := newReportDeps()
		d.tx.err = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "mark run failed") {
			t.Fatalf("expected errBoom plus mark failed error, got %v", err)
		}
		if d.run.Status() != domain.RunStatusRunning || d.run.CompletedAt() != nil || d.pub.calls != 0 {
			t.Errorf("status=%q completedAt=%v publish calls=%d", d.run.Status(), d.run.CompletedAt(), d.pub.calls)
		}
	})

	t.Run("completed and failed times come from the clock", func(t *testing.T) {
		d := newReportDeps()
		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !d.run.CompletedAt().Equal(testNow) || !d.pub.events[0].OccurredAt().Equal(testNow) {
			t.Errorf("completedAt=%v occurredAt=%v, want %v", d.run.CompletedAt(), d.pub.events[0].OccurredAt(), testNow)
		}
	})

	t.Run("error marking the run failed is reported with the cause", func(t *testing.T) {
		d := newReportDeps()
		d.reader.err = errBoom
		d.runs.updateErr = errors.New("db down")
		d.runs.updateErrAt = 2

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "mark run failed: db down") {
			t.Fatalf("expected cause plus mark failed error, got %v", err)
		}
	})
}

// ── ProcessInternal ───────────────────────────────────────────────────────────

type internalDeps struct {
	run    *domain.ReconciliationRun
	runs   *fakeRunRepo
	discs  *fakeDiscRepo
	ledger *fakeLedger
	tx     *fakeTx
	pub    *fakePublisher
}

func newInternalDeps() *internalDeps {
	run := pendingRun(tenantA, domain.TypeInternal)
	return &internalDeps{
		run:    run,
		runs:   newRunRepo(run),
		discs:  newDiscRepo(),
		ledger: &fakeLedger{},
		tx:     &fakeTx{},
		pub:    &fakePublisher{},
	}
}

func (d *internalDeps) exec() error {
	uc := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.tx, d.pub, clock)
	return uc.Execute(context.Background(), ProcessInternalCmd{TenantID: d.run.TenantID().String(), RunID: d.run.ID().String()})
}

func TestProcessInternalUseCase_Execute(t *testing.T) {
	t.Run("consistent ledger matches every entry and capture", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.check = LedgerCheck{EntriesChecked: 3, CapturesChecked: 2}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted {
			t.Errorf("status = %q, want completed", d.run.Status())
		}
		if d.run.TotalRecords() != 5 || d.run.MatchedCount() != 5 || d.run.DiscrepancyCount() != 0 {
			t.Errorf("counts = %d/%d/%d, want 5/5/0", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
		}
		if d.discs.saveCalls != 0 {
			t.Error("SaveAll should not be called when consistent")
		}
		if d.ledger.lastTenant != domain.TenantID(tenantA) || !d.ledger.lastFrom.Equal(periodFrom) || !d.ledger.lastTo.Equal(periodTo) {
			t.Errorf("ledger checked with tenant=%q from=%v to=%v", d.ledger.lastTenant, d.ledger.lastFrom, d.ledger.lastTo)
		}
		wantUpdates := []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusCompleted}
		if !reflect.DeepEqual(d.runs.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.runs.updates, wantUpdates)
		}
		if len(d.pub.events) != 1 {
			t.Errorf("events = %d, want 1", len(d.pub.events))
		}
	})

	t.Run("one discrepancy per unbalanced entry and per capture without entry", func(t *testing.T) {
		d := newInternalDeps()
		captured := periodFrom.Add(time.Hour)
		d.ledger.check = LedgerCheck{
			EntriesChecked: 4,
			Unbalanced: []LedgerImbalance{
				{EntryID: "E-SINGLE", Currency: "ARS", Imbalance: 50},
				{EntryID: "E-MIXED", Currency: "ARS", Imbalance: 100},
				{EntryID: "E-MIXED", Currency: "USD", Imbalance: -100},
				{EntryID: "E-EMPTY"},
			},
			CapturesChecked: 3,
			MissingEntries:  []SystemPayment{{PaymentID: "P-2", PSPReference: "R2", Amount: 200, Currency: "ARS", Status: "captured", CapturedAt: &captured}},
		}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.TotalRecords() != 7 || d.run.MatchedCount() != 3 || d.run.DiscrepancyCount() != 4 {
			t.Errorf("counts = %d/%d/%d, want 7/3/4", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
		}

		want := []struct {
			typ                domain.DiscrepancyType
			record, sys, extnl string
		}{
			{domain.DiscrepancyLedgerImbalance, "E-SINGLE", "0", "50 ARS (créditos - débitos)"},
			{domain.DiscrepancyLedgerImbalance, "E-MIXED", "0", "100 ARS, -100 USD (créditos - débitos)"},
			{domain.DiscrepancyLedgerImbalance, "E-EMPTY", "0", "sin postings"},
			{domain.DiscrepancyMissingInLedger, "P-2", "", ""},
		}
		if len(d.discs.saved) != len(want) {
			t.Fatalf("got %d discrepancies, want %d", len(d.discs.saved), len(want))
		}
		for i, w := range want {
			disc := d.discs.saved[i]
			if disc.Type() != w.typ || disc.RecordID() != w.record || disc.ExternalValue() != w.extnl ||
				(w.sys != "" && disc.SystemValue() != w.sys) || disc.RunID() != d.run.ID() {
				t.Errorf("discrepancy %d = type=%q record=%q system=%q external=%q", i, disc.Type(), disc.RecordID(), disc.SystemValue(), disc.ExternalValue())
			}
		}
		if !strings.Contains(d.discs.saved[3].SystemValue(), `"PaymentID":"P-2"`) {
			t.Errorf("missing_in_ledger system value = %q", d.discs.saved[3].SystemValue())
		}
	})

	t.Run("invalid run id", func(t *testing.T) {
		d := newInternalDeps()
		err := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.tx, d.pub, clock).Execute(context.Background(), ProcessInternalCmd{RunID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid run id") {
			t.Fatalf("expected invalid run id error, got %v", err)
		}
	})

	t.Run("find error is wrapped", func(t *testing.T) {
		d := newInternalDeps()
		d.runs.findErr = errBoom
		if err := d.exec(); !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "find run") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("run of another tenant is reported as not found", func(t *testing.T) {
		d := newInternalDeps()
		uc := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.tx, d.pub, clock)

		err := uc.Execute(context.Background(), ProcessInternalCmd{TenantID: tenantB, RunID: d.run.ID().String()})
		if !errors.Is(err, domain.ErrRunNotFound) {
			t.Fatalf("expected ErrRunNotFound, got %v", err)
		}
		if d.run.Status() != domain.RunStatusPending || len(d.runs.updates) != 0 {
			t.Error("foreign run must not be touched")
		}
	})

	t.Run("run not pending is rejected", func(t *testing.T) {
		d := newInternalDeps()
		d.run = domain.ReconstituteRun(domain.NewRunID(), domain.TenantID(tenantA), domain.TypeInternal, domain.RunStatusCompleted,
			periodFrom, periodTo, 0, 0, 0, "", periodTo, nil)
		d.runs = newRunRepo(d.run)
		if err := d.exec(); !errors.Is(err, domain.ErrRunAlreadyRunning) {
			t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
		}
	})

	t.Run("payment run is rejected without touching it", func(t *testing.T) {
		d := newInternalDeps()
		d.run = pendingRun(tenantA, domain.TypePayment)
		d.runs = newRunRepo(d.run)

		if err := d.exec(); !errors.Is(err, domain.ErrRunTypeMismatch) {
			t.Fatalf("expected ErrRunTypeMismatch, got %v", err)
		}
		if d.run.Status() != domain.RunStatusPending || len(d.runs.updates) != 0 {
			t.Error("run of another type must not be processed")
		}
	})

	t.Run("claim lost to a concurrent process aborts", func(t *testing.T) {
		d := newInternalDeps()
		d.runs.updateErr = domain.ErrRunAlreadyRunning
		d.runs.updateErrAt = 1

		if err := d.exec(); !errors.Is(err, domain.ErrRunAlreadyRunning) {
			t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
		}
		if d.tx.calls != 0 {
			t.Error("the loser must not complete the run")
		}
	})

	t.Run("ledger error marks run failed", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.err = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "check ledger") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.run.Status() != domain.RunStatusFailed || d.run.ErrorMsg() != "check ledger: boom" {
			t.Errorf("status=%q errorMsg=%q", d.run.Status(), d.run.ErrorMsg())
		}
		if !reflect.DeepEqual(d.runs.updates, []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusFailed}) {
			t.Errorf("updates = %v", d.runs.updates)
		}
	})

	t.Run("save discrepancies error marks run failed", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.check = LedgerCheck{EntriesChecked: 1, Unbalanced: []LedgerImbalance{{EntryID: "E", Currency: "ARS", Imbalance: 10}}}
		d.discs.saveErr = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save discrepancies") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.run.Status() != domain.RunStatusFailed {
			t.Errorf("status = %q, want failed", d.run.Status())
		}
		assertOnlyFailedEvent(t, d.pub, d.run)
	})

	t.Run("final update error rolls back and marks run failed", func(t *testing.T) {
		d := newInternalDeps()
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 2

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update run completed") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.tx.rollbacks != 1 || d.run.Status() != domain.RunStatusFailed {
			t.Errorf("rollbacks=%d status=%q", d.tx.rollbacks, d.run.Status())
		}
	})

	t.Run("publish error rolls back both the completion and the failure", func(t *testing.T) {
		d := newInternalDeps()
		d.pub.err = errBoom
		if err := d.exec(); !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "mark run failed") {
			t.Fatalf("expected errBoom plus mark failed error, got %v", err)
		}
		if d.tx.rollbacks != 2 {
			t.Errorf("rollbacks = %d, want 2", d.tx.rollbacks)
		}
	})
}

// assertOnlyFailedEvent verifica que lo único publicado sea run.failed del run.
func assertOnlyFailedEvent(t *testing.T, pub *fakePublisher, run *domain.ReconciliationRun) {
	t.Helper()
	if len(pub.events) != 1 {
		t.Fatalf("published %d events, want only run.failed", len(pub.events))
	}
	ev, ok := pub.events[0].(domain.ReconciliationFailedEvent)
	if !ok || ev.RunID != run.ID().String() || ev.Error != run.ErrorMsg() || ev.Type != run.Type().String() {
		t.Errorf("event = %+v", pub.events[0])
	}
}

func TestLifecycle_RunNotRunning(t *testing.T) {
	// Defensivo: el claim deja el run en running antes de cerrarlo. Si se
	// intentara cerrar uno que no lo está, nada se escribe y el error lo dice.
	run := pendingRun(tenantA, domain.TypePayment)
	runs, pub := newRunRepo(run), &fakePublisher{}
	l := lifecycle{runs: runs, discs: newDiscRepo(), tx: &fakeTx{}, pub: pub, clock: clock}

	err := l.complete(context.Background(), run, nil, 1, 1)
	if !errors.Is(err, domain.ErrRunNotRunning) || !strings.Contains(err.Error(), "mark run failed") {
		t.Fatalf("expected ErrRunNotRunning on both complete and fail, got %v", err)
	}
	if run.Status() != domain.RunStatusPending || len(runs.updates) != 0 || pub.calls != 0 {
		t.Error("nothing should be written")
	}
}
