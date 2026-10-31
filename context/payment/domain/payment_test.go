package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const testTenant = TenantID("11111111-1111-1111-1111-111111111111")

func testPayer() PayerInfo {
	return PayerInfo{Name: "Ana Pérez", Email: "ana@example.com", DocType: "DNI", DocNum: "30123456"}
}

// newTestPayment crea un pago en initiated por 10000 ARS con tarjeta.
func newTestPayment(t *testing.T) *Payment {
	t.Helper()
	amount, err := NewMoney(10_000, "ARS")
	if err != nil {
		t.Fatalf("NewMoney: %v", err)
	}
	p, err := NewPayment(NewPaymentID(), testTenant, "idem-1", amount, testPayer(), MethodCard, map[string]string{"order": "1"})
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	return p
}

func newProcessingPayment(t *testing.T) *Payment {
	t.Helper()
	p := newTestPayment(t)
	if err := p.MarkProcessing(ReconstituteMoney(300, "ARS")); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
	return p
}

func newCapturedPayment(t *testing.T) *Payment {
	t.Helper()
	p := newProcessingPayment(t)
	if err := p.Capture("mock", "PSP-1", ReconstituteMoney(300, "ARS"), ReconstituteMoney(150, "ARS")); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	p.PullEvents()
	return p
}

func reconstituteWithStatus(st PaymentStatus) *Payment {
	now := time.Now().UTC()
	zero := ReconstituteMoney(0, "ARS")
	return ReconstitutePayment(
		NewPaymentID(), testTenant, "idem-1", "",
		ReconstituteMoney(10_000, "ARS"), zero, zero,
		testPayer(), MethodCard,
		"", "", st, "", nil,
		nil, nil, nil,
		now, now, "", nil, "",
	)
}

// allStatusesExcept devuelve todos los estados salvo el indicado.
func allStatusesExcept(allowed PaymentStatus) []PaymentStatus {
	var out []PaymentStatus
	for _, s := range []PaymentStatus{
		StatusInitiated, StatusProcessing, StatusCaptured,
		StatusRiskRejected, StatusFailed, StatusRefunded,
	} {
		if s != allowed {
			out = append(out, s)
		}
	}
	return out
}

func TestNewPayment(t *testing.T) {
	t.Run("creates initiated payment with zero fees", func(t *testing.T) {
		id := NewPaymentID()
		amount, _ := NewMoney(10_000, "USD")
		meta := map[string]string{"order": "1"}
		before := time.Now().UTC()

		p, err := NewPayment(id, testTenant, "idem-1", amount, testPayer(), MethodWallet, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if p.ID() != id || p.TenantID() != testTenant || p.IdempotencyKey() != "idem-1" {
			t.Errorf("identity not set: %v %v %q", p.ID(), p.TenantID(), p.IdempotencyKey())
		}
		if p.Amount() != amount || p.Method() != MethodWallet || p.PayerInfo() != testPayer() {
			t.Errorf("payment data not set")
		}
		if !reflect.DeepEqual(p.Metadata(), meta) {
			t.Errorf("metadata = %v", p.Metadata())
		}
		if p.Status() != StatusInitiated {
			t.Errorf("status = %q, want initiated", p.Status())
		}
		if !p.PlatformFee().IsZero() || p.PlatformFee().Currency() != "USD" ||
			!p.PSPFee().IsZero() || p.PSPFee().Currency() != "USD" {
			t.Errorf("fees should be zero in payment currency: %v / %v", p.PlatformFee(), p.PSPFee())
		}
		if p.NetAmount() != 10_000 {
			t.Errorf("net amount = %d, want 10000", p.NetAmount())
		}
		if p.CheckoutID() != "" || p.PSPName() != "" || p.PSPReference() != "" || p.FailureReason() != "" {
			t.Error("optional strings should be empty")
		}
		if p.AuthorizedAt() != nil || p.CapturedAt() != nil || p.FailedAt() != nil {
			t.Error("lifecycle timestamps should be nil")
		}
		if p.CreatedAt().Before(before) || !p.CreatedAt().Equal(p.UpdatedAt()) || p.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt=%v updatedAt=%v", p.CreatedAt(), p.UpdatedAt())
		}
		if len(p.PullEvents()) != 0 {
			t.Error("creation should not emit events")
		}
	})

	t.Run("empty idempotency key rejected", func(t *testing.T) {
		amount, _ := NewMoney(100, "ARS")
		p, err := NewPayment(NewPaymentID(), testTenant, "", amount, testPayer(), MethodCard, nil)
		if err == nil || p != nil {
			t.Fatalf("expected error and nil payment, got %v, %v", p, err)
		}
	})
}

func TestPayment_MarkProcessing(t *testing.T) {
	t.Run("from initiated", func(t *testing.T) {
		p := newTestPayment(t)
		created := p.UpdatedAt()

		if err := p.MarkProcessing(ReconstituteMoney(300, "ARS")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusProcessing {
			t.Errorf("status = %q, want processing", p.Status())
		}
		if p.UpdatedAt().Before(created) {
			t.Error("updatedAt should advance")
		}
		if len(p.PullEvents()) != 0 {
			t.Error("MarkProcessing should not emit events")
		}
	})

	for _, st := range allStatusesExcept(StatusInitiated) {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.MarkProcessing(ReconstituteMoney(300, "ARS")); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st {
				t.Errorf("status changed to %q", p.Status())
			}
		})
	}
}

func TestPayment_Capture(t *testing.T) {
	t.Run("from processing", func(t *testing.T) {
		p := newProcessingPayment(t)
		platformFee := ReconstituteMoney(300, "ARS")
		pspFee := ReconstituteMoney(150, "ARS")

		if err := p.Capture("mock", "PSP-1", platformFee, pspFee); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusCaptured {
			t.Errorf("status = %q, want captured", p.Status())
		}
		if p.PSPName() != "mock" || p.PSPReference() != "PSP-1" {
			t.Errorf("psp = %q / %q", p.PSPName(), p.PSPReference())
		}
		if p.PlatformFee() != platformFee || p.PSPFee() != pspFee {
			t.Errorf("fees = %v / %v", p.PlatformFee(), p.PSPFee())
		}
		if p.NetAmount() != 10_000-300-150 {
			t.Errorf("net amount = %d", p.NetAmount())
		}
		if p.CapturedAt() == nil || !p.UpdatedAt().Equal(*p.CapturedAt()) {
			t.Errorf("capturedAt=%v updatedAt=%v", p.CapturedAt(), p.UpdatedAt())
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PaymentCapturedEvent)
		if !ok {
			t.Fatalf("expected PaymentCapturedEvent, got %T", evs[0])
		}
		want := PaymentCapturedEvent{
			baseEvent:      ev.baseEvent,
			PaymentID:      p.ID().String(),
			TenantID:       testTenant.String(),
			Amount:         10_000,
			Currency:       "ARS",
			PlatformFee:    300,
			PSPFee:         150,
			PaymentMethod:  "card",
			PSPReference:   "PSP-1",
			IdempotencyKey: "idem-1",
		}
		if ev != want {
			t.Errorf("event = %+v\nwant %+v", ev, want)
		}
		if ev.EventTenantID() != testTenant.String() {
			t.Errorf("event tenant = %q", ev.EventTenantID())
		}
	})

	for _, st := range allStatusesExcept(StatusProcessing) {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			err := p.Capture("mock", "PSP-1", ReconstituteMoney(1, "ARS"), ReconstituteMoney(1, "ARS"))
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st || p.PSPReference() != "" || len(p.PullEvents()) != 0 {
				t.Error("rejected transition must not change state or emit events")
			}
		})
	}
}

func TestPayment_RejectByRisk(t *testing.T) {
	t.Run("from initiated", func(t *testing.T) {
		p := newTestPayment(t)

		if err := p.RejectByRisk("velocity"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusRiskRejected || p.FailureReason() != "velocity" {
			t.Errorf("status=%q reason=%q", p.Status(), p.FailureReason())
		}
		if p.FailedAt() == nil || !p.UpdatedAt().Equal(*p.FailedAt()) {
			t.Errorf("failedAt=%v updatedAt=%v", p.FailedAt(), p.UpdatedAt())
		}
		if p.PSPName() != "" {
			t.Error("risk rejection should not set psp name")
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PaymentFailedEvent)
		if !ok {
			t.Fatalf("expected PaymentFailedEvent, got %T", evs[0])
		}
		if ev.PaymentID != p.ID().String() || ev.TenantID != testTenant.String() || ev.Amount != 10_000 ||
			ev.Currency != "ARS" || ev.FailureReason != "velocity" || ev.PaymentMethod != "card" {
			t.Errorf("event fields not set: %+v", ev)
		}
	})

	for _, st := range allStatusesExcept(StatusInitiated) {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.RejectByRisk("x"); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st || len(p.PullEvents()) != 0 {
				t.Error("rejected transition must not change state or emit events")
			}
		})
	}
}

func TestPayment_Fail(t *testing.T) {
	t.Run("from processing", func(t *testing.T) {
		p := newProcessingPayment(t)

		if err := p.Fail("mock", "declined by the payment provider", "card 4111 declined"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusFailed || p.FailureReason() != "declined by the payment provider" || p.PSPName() != "mock" {
			t.Errorf("status=%q reason=%q psp=%q", p.Status(), p.FailureReason(), p.PSPName())
		}
		// El error crudo del PSP queda solo para diagnóstico.
		if p.PSPFailureDetail() != "card 4111 declined" {
			t.Errorf("psp failure detail = %q", p.PSPFailureDetail())
		}
		if p.FailedAt() == nil || !p.UpdatedAt().Equal(*p.FailedAt()) {
			t.Errorf("failedAt=%v updatedAt=%v", p.FailedAt(), p.UpdatedAt())
		}
		if p.CapturedAt() != nil {
			t.Error("capturedAt should stay nil")
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PaymentFailedEvent)
		if !ok {
			t.Fatalf("expected PaymentFailedEvent, got %T", evs[0])
		}
		if ev.PaymentID != p.ID().String() || ev.TenantID != testTenant.String() || ev.Amount != 10_000 ||
			ev.Currency != "ARS" || ev.FailureReason != "declined by the payment provider" || ev.PaymentMethod != "card" {
			t.Errorf("event fields not set: %+v", ev)
		}
	})

	for _, st := range allStatusesExcept(StatusProcessing) {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.Fail("mock", "x", ""); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st || len(p.PullEvents()) != 0 {
				t.Error("rejected transition must not change state or emit events")
			}
		})
	}
}

func TestPayment_Refund(t *testing.T) {
	t.Run("from captured", func(t *testing.T) {
		p := newCapturedPayment(t)
		capturedUpdate := p.UpdatedAt()

		if err := p.Refund("REFUND-1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusRefunded {
			t.Errorf("status = %q, want refunded", p.Status())
		}
		if p.UpdatedAt().Before(capturedUpdate) {
			t.Error("updatedAt should advance")
		}
		if p.PSPReference() != "PSP-1" {
			t.Errorf("original psp reference should be kept, got %q", p.PSPReference())
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PaymentRefundedEvent)
		if !ok {
			t.Fatalf("expected PaymentRefundedEvent, got %T", evs[0])
		}
		if ev.PaymentID != p.ID().String() || ev.TenantID != testTenant.String() ||
			ev.RefundAmount != 10_000 || ev.Currency != "ARS" || ev.PSPReference != "REFUND-1" {
			t.Errorf("event fields not set: %+v", ev)
		}
	})

	for _, st := range allStatusesExcept(StatusCaptured) {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.Refund("R"); !errors.Is(err, ErrNotCaptured) {
				t.Fatalf("expected ErrNotCaptured, got %v", err)
			}
			if p.Status() != st || len(p.PullEvents()) != 0 {
				t.Error("rejected refund must not change state or emit events")
			}
		})
	}
}

func TestPayment_PullEvents(t *testing.T) {
	p := newProcessingPayment(t)
	_ = p.Capture("mock", "PSP-1", ReconstituteMoney(0, "ARS"), ReconstituteMoney(0, "ARS"))
	_ = p.Refund("R-1")

	evs := p.PullEvents()
	if len(evs) != 2 || evs[0].EventType() != "payment.captured.v1" || evs[1].EventType() != "payment.refunded.v1" {
		t.Fatalf("unexpected events: %v", evs)
	}
	if len(p.PullEvents()) != 0 {
		t.Error("second pull should be empty")
	}
}

func TestReconstitutePayment(t *testing.T) {
	id := NewPaymentID()
	authorized := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	captured := authorized.Add(time.Second)
	created := authorized.Add(-time.Minute)
	meta := map[string]string{"k": "v"}

	p := ReconstitutePayment(
		id, testTenant, "idem-9", "chk-1",
		ReconstituteMoney(5000, "USD"), ReconstituteMoney(100, "USD"), ReconstituteMoney(50, "USD"),
		testPayer(), MethodQR,
		"mercadopago", "MP-1", StatusCaptured, "", meta,
		&authorized, &captured, nil,
		created, captured, "", nil, "",
	)

	if p.ID() != id || p.TenantID() != testTenant || p.IdempotencyKey() != "idem-9" || p.CheckoutID() != "chk-1" {
		t.Errorf("identity not restored")
	}
	if p.Amount().Amount() != 5000 || p.PlatformFee().Amount() != 100 || p.PSPFee().Amount() != 50 || p.NetAmount() != 4850 {
		t.Errorf("amounts not restored: %v %v %v", p.Amount(), p.PlatformFee(), p.PSPFee())
	}
	if p.PayerInfo() != testPayer() || p.Method() != MethodQR || !reflect.DeepEqual(p.Metadata(), meta) {
		t.Errorf("payer/method/metadata not restored")
	}
	if p.PSPName() != "mercadopago" || p.PSPReference() != "MP-1" || p.Status() != StatusCaptured {
		t.Errorf("psp/status not restored")
	}
	if p.AuthorizedAt() != &authorized || p.CapturedAt() != &captured || p.FailedAt() != nil {
		t.Error("lifecycle timestamps not restored")
	}
	if !p.CreatedAt().Equal(created) || !p.UpdatedAt().Equal(captured) {
		t.Errorf("createdAt=%v updatedAt=%v", p.CreatedAt(), p.UpdatedAt())
	}
	if len(p.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

// Comisiones negativas o que dejan el neto ≤ 0: Ledger rechaza ese asiento y
// el pago quedaba captured sin contabilizar.
func TestPayment_FeeValidation(t *testing.T) {
	ars := func(n int64) Money { return ReconstituteMoney(n, "ARS") }
	amount := newTestPayment(t).Amount().Amount()

	for name, fee := range map[string]int64{"negative": -1, "equal to amount": amount, "above amount": amount + 1} {
		t.Run("MarkProcessing rejects platform fee "+name, func(t *testing.T) {
			p := newTestPayment(t)
			if err := p.MarkProcessing(ars(fee)); !errors.Is(err, ErrInvalidFee) {
				t.Fatalf("expected ErrInvalidFee, got %v", err)
			}
			if p.Status() != StatusInitiated {
				t.Errorf("status = %q, want initiated", p.Status())
			}
		})
	}

	for name, tc := range map[string]struct{ platform, psp int64 }{
		"negative psp fee":      {300, -1},
		"fees equal to amount":  {300, amount - 300},
		"fees above amount":     {300, amount},
		"negative platform fee": {-1, 10},
	} {
		t.Run("Capture rejects "+name, func(t *testing.T) {
			p := newProcessingPayment(t)
			if err := p.Capture("mock", "PSP-1", ars(tc.platform), ars(tc.psp)); !errors.Is(err, ErrInvalidFee) {
				t.Fatalf("expected ErrInvalidFee, got %v", err)
			}
			if p.Status() != StatusProcessing || len(p.PullEvents()) != 0 {
				t.Errorf("status = %q, want processing without events", p.Status())
			}
		})
	}

	t.Run("fees leaving a positive net are accepted", func(t *testing.T) {
		p := newProcessingPayment(t)
		if err := p.Capture("mock", "PSP-1", ars(300), ars(amount-301)); err != nil || p.NetAmount() != 1 {
			t.Fatalf("err = %v net = %d", err, p.NetAmount())
		}
	})
}

// El rechazo por riesgo y el del PSP emitían el mismo payment.failed.v1 sin
// forma de distinguirlos.
func TestPaymentFailedEvent_FailureStage(t *testing.T) {
	risk := newTestPayment(t)
	_ = risk.RejectByRisk("score too high")
	psp := newProcessingPayment(t)
	_ = psp.Fail("mock", FailureDeclinedByPSP, "")
	for want, p := range map[string]*Payment{FailureStageRisk: risk, FailureStagePSP: psp} {
		evs := p.PullEvents()
		if len(evs) != 1 || evs[0].(PaymentFailedEvent).FailureStage != want {
			t.Errorf("failure stage = %+v, want %q", evs, want)
		}
	}
}
