package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

type processDeps struct {
	repo   *fakeRepo
	gate   *fakeGate
	psp    *fakePSP
	router *fakeRouter
	risk   *fakeRisk
	fees   *fakeFees
	tx     *fakeTx
	pub    *fakePublisher
}

func newProcessDeps() *processDeps {
	psp := &fakePSP{captureResult: PSPCaptureResult{PSPReference: "PSP-REF-1", PSPFee: 150, Status: "approved"}}
	return &processDeps{
		repo:   &fakeRepo{},
		gate:   &fakeGate{},
		psp:    psp,
		router: &fakeRouter{psp: psp},
		risk:   &fakeRisk{decision: RiskDecision{Approved: true}},
		fees:   &fakeFees{fee: 300},
		tx:     &fakeTx{},
		pub:    &fakePublisher{},
	}
}

func (d *processDeps) uc() *ProcessPaymentUseCase {
	return NewProcessPaymentUseCase(d.repo, d.gate, d.router, d.risk, d.fees, d.tx, d.pub)
}

func validProcessCmd() ProcessPaymentCmd {
	return ProcessPaymentCmd{
		TenantID:       tenantA,
		IdempotencyKey: "order-123",
		Amount:         10_000,
		Currency:       "ars",
		PaymentMethod:  "card",
		PaymentToken:   "tok_abc",
		Description:    "Pedido 123",
		PayerName:      "Ana Pérez",
		PayerEmail:     "ana@example.com",
		PayerDocType:   "DNI",
		PayerDocNum:    "30123456",
		Metadata:       map[string]string{"order": "123"},
	}
}

func TestProcessPaymentUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path captures payment and publishes captured event", func(t *testing.T) {
		d := newProcessDeps()

		res, err := d.uc().Execute(ctx, validProcessCmd())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.Status != domain.StatusCaptured.String() || res.WasExisting {
			t.Errorf("unexpected status/existing: %+v", res)
		}
		if res.Amount != 10_000 || res.Currency != "ARS" || res.PlatformFee != 300 {
			t.Errorf("unexpected amounts: %+v", res)
		}
		if res.PSPReference != "PSP-REF-1" || res.CapturedAt == nil {
			t.Errorf("capture data missing: %+v", res)
		}
		if _, err := domain.ParsePaymentID(res.PaymentID); err != nil {
			t.Errorf("payment id is not a uuid: %v", err)
		}

		if len(d.repo.saved) != 1 {
			t.Fatalf("expected 1 save, got %d", len(d.repo.saved))
		}
		p := d.repo.saved[0]
		if p.PSPName() != "fakepsp" || p.PSPFee().Amount() != 150 || p.PSPFee().Currency() != "ARS" {
			t.Errorf("psp data not stored: name=%q fee=%v", p.PSPName(), p.PSPFee())
		}
		if p.NetAmount() != 10_000-300-150 {
			t.Errorf("net amount = %d", p.NetAmount())
		}

		wantUpdates := []domain.PaymentStatus{domain.StatusProcessing, domain.StatusCaptured}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.captured.v1"}) {
			t.Errorf("events = %v", got)
		}
		if d.tx.calls != 1 {
			t.Errorf("tx calls = %d, want 1", d.tx.calls)
		}
	})

	t.Run("dependencies receive parsed inputs", func(t *testing.T) {
		d := newProcessDeps()

		if _, err := d.uc().Execute(ctx, validProcessCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.repo.lastKey != "order-123" || d.repo.lastKeyTenant != domain.TenantID(tenantA) {
			t.Errorf("idempotency lookup with key=%q tenant=%q", d.repo.lastKey, d.repo.lastKeyTenant)
		}
		if d.risk.lastStatus != domain.StatusInitiated {
			t.Errorf("risk evaluated payment in status %q, want initiated", d.risk.lastStatus)
		}
		if d.fees.lastAmount.Amount() != 10_000 || d.fees.lastAmount.Currency() != "ARS" || d.fees.lastMethod != domain.MethodCard {
			t.Errorf("fee calculated with %v / %q", d.fees.lastAmount, d.fees.lastMethod)
		}
		if d.router.lastMethod != domain.MethodCard || d.router.lastTenant != domain.TenantID(tenantA) {
			t.Errorf("routed with %q / %q", d.router.lastMethod, d.router.lastTenant)
		}
	})

	t.Run("capture request is built from command and payment", func(t *testing.T) {
		d := newProcessDeps()

		res, err := d.uc().Execute(ctx, validProcessCmd())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.psp.captures) != 1 {
			t.Fatalf("expected 1 capture, got %d", len(d.psp.captures))
		}
		want := PSPCaptureRequest{
			PaymentID:      res.PaymentID,
			IdempotencyKey: "order-123",
			Amount:         10_000,
			Currency:       "ARS",
			PaymentMethod:  "card",
			PaymentToken:   "tok_abc",
			Description:    "Pedido 123",
			PayerInfo:      testPayer(),
			Metadata:       map[string]string{"order": "123"},
		}
		if got := d.psp.captures[0]; !reflect.DeepEqual(got, want) {
			t.Errorf("capture request = %+v\nwant %+v", got, want)
		}
	})

	t.Run("existing payment is returned without reprocessing", func(t *testing.T) {
		d := newProcessDeps()
		existing := buildPayment(tenantA, domain.StatusCaptured)
		d.repo.byKey = existing

		res, err := d.uc().Execute(ctx, validProcessCmd())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.WasExisting || res.PaymentID != existing.ID().String() {
			t.Errorf("expected existing payment, got %+v", res)
		}
		if res.Status != "captured" || res.PlatformFee != 300 || res.PSPReference != "PSP-REF-1" {
			t.Errorf("existing payment not mapped: %+v", res)
		}
		if len(d.repo.saved) != 0 || d.gate.calls != 0 || d.risk.calls != 0 || len(d.psp.captures) != 0 || d.pub.calls != 0 {
			t.Error("idempotent retry must not touch any dependency")
		}
	})

	t.Run("idempotency lookup error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.findKeyErr = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "check idempotency") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.repo.saved) != 0 {
			t.Error("nothing should be saved")
		}
	})

	validation := []struct {
		name    string
		mutate  func(*ProcessPaymentCmd)
		wantErr error
		wantMsg string
	}{
		{"invalid tenant", func(c *ProcessPaymentCmd) { c.TenantID = "nope" }, nil, "invalid tenant id"},
		{"missing idempotency key", func(c *ProcessPaymentCmd) { c.IdempotencyKey = "" }, nil, "idempotency_key is required"},
		{"zero amount", func(c *ProcessPaymentCmd) { c.Amount = 0 }, domain.ErrInvalidAmount, ""},
		{"invalid currency", func(c *ProcessPaymentCmd) { c.Currency = "PESOS" }, domain.ErrInvalidCurrency, ""},
		{"invalid method", func(c *ProcessPaymentCmd) { c.PaymentMethod = "cash" }, domain.ErrInvalidMethod, ""},
	}
	for _, tc := range validation {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			d := newProcessDeps()
			cmd := validProcessCmd()
			tc.mutate(&cmd)

			_, err := d.uc().Execute(ctx, cmd)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			if tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
			if d.repo.keyLookups != 0 || len(d.repo.saved) != 0 {
				t.Error("repo must not be touched on validation error")
			}
		})
	}

	t.Run("save error aborts before risk", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.saveErr = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save payment") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.risk.calls != 0 {
			t.Error("risk should not be evaluated when save fails")
		}
	})

	t.Run("risk evaluation error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.risk.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "risk evaluation") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.fees.calls != 0 || len(d.psp.captures) != 0 {
			t.Error("flow should stop on risk error")
		}
	})

	t.Run("risk rejection persists rejection and publishes failed event", func(t *testing.T) {
		d := newProcessDeps()
		d.risk.decision = RiskDecision{Approved: false, Reason: "velocity", Score: 95}

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, domain.ErrRiskRejected) {
			t.Fatalf("expected ErrRiskRejected, got %v", err)
		}

		p := d.repo.saved[0]
		if p.Status() != domain.StatusRiskRejected || p.FailureReason() != "velocity" || p.FailedAt() == nil {
			t.Errorf("payment not rejected: status=%q reason=%q", p.Status(), p.FailureReason())
		}
		if !reflect.DeepEqual(d.repo.updates, []domain.PaymentStatus{domain.StatusRiskRejected}) {
			t.Errorf("updates = %v", d.repo.updates)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.failed.v1"}) {
			t.Errorf("events = %v", got)
		}
		if d.fees.calls != 0 || d.router.calls != 0 || len(d.psp.captures) != 0 {
			t.Error("rejected payment must not reach fees or PSP")
		}
	})

	t.Run("risk rejection persist error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.risk.decision = RiskDecision{Approved: false, Reason: "velocity"}
		d.repo.updateErr = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "persist risk rejection") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if errors.Is(err, domain.ErrRiskRejected) {
			t.Error("persistence failure should take precedence over ErrRiskRejected")
		}
		if d.pub.calls != 0 {
			t.Error("event must not be published when update fails")
		}
	})

	t.Run("risk rejection publish error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.risk.decision = RiskDecision{Approved: false, Reason: "velocity"}
		d.pub.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "persist risk rejection") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("fee calculation error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.fees.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "calculate fee") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.router.calls != 0 || len(d.repo.updates) != 0 {
			t.Error("flow should stop on fee error")
		}
	})

	t.Run("routing error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.router.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "route psp") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.repo.updates) != 0 {
			t.Error("payment should stay initiated when routing fails")
		}
	})

	t.Run("update to processing error aborts before capture", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 1

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update to processing") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.psp.captures) != 0 {
			t.Error("PSP must not be called when processing update fails")
		}
	})

	t.Run("psp rejection fails payment and publishes failed event", func(t *testing.T) {
		d := newProcessDeps()
		d.psp.captureErr = errors.New("card declined")

		res, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, domain.ErrPaymentDeclined) || !errors.Is(err, d.psp.captureErr) {
			t.Fatalf("expected ErrPaymentDeclined wrapping the psp error, got %v", err)
		}
		// El resultado acompaña al error: el handler lo usa para responder 402.
		if res.Status != domain.StatusFailed.String() || res.PaymentID != d.repo.saved[0].ID().String() {
			t.Errorf("declined result not returned: %+v", res)
		}

		p := d.repo.saved[0]
		if p.Status() != domain.StatusFailed || p.FailureReason() != "card declined" || p.PSPName() != "fakepsp" {
			t.Errorf("payment not failed: status=%q reason=%q psp=%q", p.Status(), p.FailureReason(), p.PSPName())
		}
		wantUpdates := []domain.PaymentStatus{domain.StatusProcessing, domain.StatusFailed}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.failed.v1"}) {
			t.Errorf("events = %v", got)
		}
	})

	t.Run("psp rejection persist error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.psp.captureErr = errors.New("card declined")
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "persist psp failure") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("capture update error is wrapped as persist capture", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "persist capture") || !strings.Contains(err.Error(), "update captured") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("captured event must not be published when update fails")
		}
	})

	t.Run("capture publish error is wrapped as persist capture", func(t *testing.T) {
		d := newProcessDeps()
		d.pub.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "persist capture") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("capture tx error is wrapped as persist capture", func(t *testing.T) {
		d := newProcessDeps()
		d.tx.err = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "persist capture") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.psp.captures) != 1 {
			t.Error("PSP capture happens before the tx")
		}
	})
}

// ── Habilitación del tenant ───────────────────────────────────────────────────

func TestProcessPayment_TenantGate(t *testing.T) {
	ctx := context.Background()

	t.Run("inactive tenant cannot create payments", func(t *testing.T) {
		d := newProcessDeps()
		d.gate.err = domain.ErrTenantNotActive

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, domain.ErrTenantNotActive) {
			t.Fatalf("expected ErrTenantNotActive, got %v", err)
		}
		if len(d.repo.saved) != 0 || d.risk.calls != 0 || len(d.psp.captures) != 0 {
			t.Error("nothing should be persisted or processed for an inactive tenant")
		}
	})

	t.Run("inactive tenant cannot resume a stuck payment", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.byKey = buildPayment(tenantA, domain.StatusInitiated)
		d.gate.err = domain.ErrTenantNotActive

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, domain.ErrTenantNotActive) {
			t.Fatalf("expected ErrTenantNotActive, got %v", err)
		}
		if len(d.psp.captures) != 0 {
			t.Error("PSP must not be called")
		}
	})
}

// ── Reanudación de pagos trabados en initiated ────────────────────────────────

func TestProcessPayment_ResumesInitiatedPayment(t *testing.T) {
	ctx := context.Background()

	t.Run("retry processes a payment stuck in initiated", func(t *testing.T) {
		d := newProcessDeps()
		stuck := buildPayment(tenantA, domain.StatusInitiated)
		d.repo.byKey = stuck

		cmd := validProcessCmd()
		cmd.Amount = 99_999 // un reintento con otro monto no altera el pago existente
		res, err := d.uc().Execute(ctx, cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.PaymentID != stuck.ID().String() || res.Status != domain.StatusCaptured.String() || !res.WasExisting {
			t.Errorf("expected the stuck payment captured as existing, got %+v", res)
		}
		if len(d.repo.saved) != 0 {
			t.Error("a resumed payment must not be saved again")
		}
		if len(d.psp.captures) != 1 || d.psp.captures[0].Amount != 10_000 || d.psp.captures[0].PaymentID != stuck.ID().String() {
			t.Errorf("PSP must be called once with the persisted payment: %+v", d.psp.captures)
		}
		wantUpdates := []domain.PaymentStatus{domain.StatusProcessing, domain.StatusCaptured}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.captured.v1"}) {
			t.Errorf("events = %v", got)
		}
	})

	t.Run("fee error leaves the payment initiated so a retry can resume it", func(t *testing.T) {
		d := newProcessDeps()
		d.fees.err = errBoom

		if _, err := d.uc().Execute(ctx, validProcessCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		stuck := d.repo.saved[0]
		if stuck.Status() != domain.StatusInitiated {
			t.Fatalf("status = %q, want initiated", stuck.Status())
		}

		// Billing se recupera y el cliente reintenta con la misma clave.
		d.fees.err = nil
		d.repo.byKey = stuck
		res, err := d.uc().Execute(ctx, validProcessCmd())
		if err != nil {
			t.Fatalf("retry: unexpected error: %v", err)
		}
		if res.Status != domain.StatusCaptured.String() || res.PaymentID != stuck.ID().String() {
			t.Errorf("retry did not capture the stuck payment: %+v", res)
		}
	})

	t.Run("lost claim does not call the PSP and reports current state", func(t *testing.T) {
		d := newProcessDeps()
		stuck := buildPayment(tenantA, domain.StatusInitiated)
		d.repo.byKey = stuck
		d.repo.claimLost = true
		// Lo que ve FindByID: la otra ejecución ya lo pasó a processing.
		current := buildPayment(tenantA, domain.StatusProcessing)
		d.repo.byID = map[domain.PaymentID]*domain.Payment{stuck.ID(): current}

		res, err := d.uc().Execute(ctx, validProcessCmd())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.psp.captures) != 0 {
			t.Error("PSP must not be called when another execution holds the claim")
		}
		if res.Status != domain.StatusProcessing.String() || !res.WasExisting {
			t.Errorf("expected current processing state, got %+v", res)
		}
		if d.pub.calls != 0 {
			t.Error("no events should be published")
		}
	})

	t.Run("lost claim reload error is wrapped", func(t *testing.T) {
		d := newProcessDeps()
		d.repo.byKey = buildPayment(tenantA, domain.StatusInitiated)
		d.repo.claimLost = true
		d.repo.findErr = errBoom

		_, err := d.uc().Execute(ctx, validProcessCmd())
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "reload claimed payment") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
