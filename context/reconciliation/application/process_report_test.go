package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

var testReport = []byte("transaction_id,amount,currency,status\n")

type reportDeps struct {
	run    *domain.ReconciliationRun
	runs   *fakeRunRepo
	discs  *fakeDiscRepo
	reader *fakePaymentReader
	parser *fakeParser
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
		pub:    &fakePublisher{},
	}
}

func (d *reportDeps) exec() error {
	uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.pub)
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
		if d.run.TotalRecords() != 4 || d.run.MatchedCount() != 1 || d.run.DiscrepancyCount() != 4 {
			t.Errorf("counts = %d/%d/%d, want 4/1/4", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
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
		{"captured", "rejected", true},
		{"captured", "failed", true},
		{"failed", "captured", true},
		{"captured", "captured", false},
		{"captured", "refunded", false},
		{"refunded", "captured", false},
		{"failed", "rejected", false},
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

	t.Run("system payments without psp reference are skipped but counted in total", func(t *testing.T) {
		d := newReportDeps()
		d.reader.payments = []SystemPayment{sysPay("A", 100, "captured"), sysPay("", 100, "failed"), sysPay("", 50, "failed")}
		d.parser.records = []PSPRecord{pspRec("A", 100, "captured")}

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.discs.saved) != 0 {
			t.Errorf("payments without reference should not create discrepancies, got %d", len(d.discs.saved))
		}
		if d.run.TotalRecords() != 3 || d.run.MatchedCount() != 1 {
			t.Errorf("total=%d matched=%d, want 3/1", d.run.TotalRecords(), d.run.MatchedCount())
		}
	})

	t.Run("total is the larger side", func(t *testing.T) {
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
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.pub)

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
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.pub)

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
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.pub)

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
		uc := NewProcessReportUseCase(d.runs, d.discs, d.reader, d.parser, d.pub)

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

	for _, st := range []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusCompleted, domain.RunStatusFailed} {
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

	t.Run("update to running error aborts", func(t *testing.T) {
		d := newReportDeps()
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 1

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update run to running") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.reader.calls != 0 {
			t.Error("comparison should not run")
		}
	})

	failures := []struct {
		name    string
		setup   func(*reportDeps)
		wantMsg string
	}{
		{"parse error", func(d *reportDeps) { d.parser.err = errBoom }, "compare: parse report"},
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
			if d.pub.calls != 0 {
				t.Error("no event should be published on failure")
			}
		})
	}

	t.Run("update completed error is wrapped", func(t *testing.T) {
		d := newReportDeps()
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 2

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update run completed") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("event should not be published when update fails")
		}
	})

	t.Run("publish error is ignored", func(t *testing.T) {
		d := newReportDeps()
		d.pub.err = errBoom

		if err := d.exec(); err != nil {
			t.Fatalf("publish error should be swallowed, got %v", err)
		}
		if d.pub.calls != 1 {
			t.Errorf("publish calls = %d, want 1", d.pub.calls)
		}
	})
}

// ── ProcessInternal ───────────────────────────────────────────────────────────

type internalDeps struct {
	run    *domain.ReconciliationRun
	runs   *fakeRunRepo
	discs  *fakeDiscRepo
	ledger *fakeLedger
	pub    *fakePublisher
}

func newInternalDeps() *internalDeps {
	run := pendingRun(tenantA, domain.TypeInternal)
	return &internalDeps{
		run:    run,
		runs:   newRunRepo(run),
		discs:  newDiscRepo(),
		ledger: &fakeLedger{},
		pub:    &fakePublisher{},
	}
}

func (d *internalDeps) exec() error {
	uc := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.pub)
	return uc.Execute(context.Background(), ProcessInternalCmd{TenantID: d.run.TenantID().String(), RunID: d.run.ID().String()})
}

func TestProcessInternalUseCase_Execute(t *testing.T) {
	t.Run("balanced ledger completes with one match", func(t *testing.T) {
		d := newInternalDeps()

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted {
			t.Errorf("status = %q, want completed", d.run.Status())
		}
		if d.run.TotalRecords() != 1 || d.run.MatchedCount() != 1 || d.run.DiscrepancyCount() != 0 {
			t.Errorf("counts = %d/%d/%d, want 1/1/0", d.run.TotalRecords(), d.run.MatchedCount(), d.run.DiscrepancyCount())
		}
		if d.discs.saveCalls != 0 {
			t.Error("SaveAll should not be called when balanced")
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

	t.Run("imbalance creates ledger_imbalance discrepancy", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.imbalance = -500

		if err := d.exec(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.run.MatchedCount() != 0 || d.run.DiscrepancyCount() != 1 {
			t.Errorf("matched=%d discrepancies=%d, want 0/1", d.run.MatchedCount(), d.run.DiscrepancyCount())
		}
		if len(d.discs.saved) != 1 {
			t.Fatalf("expected 1 discrepancy, got %d", len(d.discs.saved))
		}
		disc := d.discs.saved[0]
		if disc.Type() != domain.DiscrepancyLedgerImbalance || disc.RecordID() != "global" ||
			disc.SystemValue() != "0" || disc.ExternalValue() != "-500 (centavos fuera de balance)" {
			t.Errorf("discrepancy = type=%q record=%q system=%q external=%q",
				disc.Type(), disc.RecordID(), disc.SystemValue(), disc.ExternalValue())
		}
		if disc.RunID() != d.run.ID() {
			t.Error("discrepancy not linked to run")
		}
	})

	t.Run("invalid run id", func(t *testing.T) {
		d := newInternalDeps()
		err := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.pub).Execute(context.Background(), ProcessInternalCmd{RunID: "x"})
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
		uc := NewProcessInternalUseCase(d.runs, d.discs, d.ledger, d.pub)

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
		d.run = runWithStatus(domain.RunStatusCompleted)
		d.runs = newRunRepo(d.run)
		if err := d.exec(); !errors.Is(err, domain.ErrRunAlreadyRunning) {
			t.Fatalf("expected ErrRunAlreadyRunning, got %v", err)
		}
	})

	t.Run("update to running error is ignored", func(t *testing.T) {
		d := newInternalDeps()
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 1

		if err := d.exec(); err != nil {
			t.Fatalf("first update error should be swallowed, got %v", err)
		}
		if d.run.Status() != domain.RunStatusCompleted {
			t.Errorf("status = %q, want completed", d.run.Status())
		}
	})

	t.Run("ledger error marks run failed", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.err = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "check ledger balance") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.run.Status() != domain.RunStatusFailed || d.run.ErrorMsg() != "boom" {
			t.Errorf("status=%q errorMsg=%q", d.run.Status(), d.run.ErrorMsg())
		}
		if !reflect.DeepEqual(d.runs.updates, []domain.RunStatus{domain.RunStatusRunning, domain.RunStatusFailed}) {
			t.Errorf("updates = %v", d.runs.updates)
		}
	})

	t.Run("save discrepancies error marks run failed", func(t *testing.T) {
		d := newInternalDeps()
		d.ledger.imbalance = 10
		d.discs.saveErr = errBoom

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save discrepancies") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.run.Status() != domain.RunStatusFailed {
			t.Errorf("status = %q, want failed", d.run.Status())
		}
		if d.pub.calls != 0 {
			t.Error("no event should be published on failure")
		}
	})

	t.Run("final update error is wrapped", func(t *testing.T) {
		d := newInternalDeps()
		d.runs.updateErr = errBoom
		d.runs.updateErrAt = 2

		err := d.exec()
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update run") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("publish error is ignored", func(t *testing.T) {
		d := newInternalDeps()
		d.pub.err = errBoom
		if err := d.exec(); err != nil {
			t.Fatalf("publish error should be swallowed, got %v", err)
		}
	})
}
