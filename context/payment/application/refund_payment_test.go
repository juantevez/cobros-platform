package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

type refundDeps struct {
	repo   *fakeRepo
	psp    *fakePSP
	router *fakeRouter
	tx     *fakeTx
	pub    *fakePublisher
}

func newRefundDeps(seed ...*domain.Payment) *refundDeps {
	psp := &fakePSP{refundResult: PSPRefundResult{PSPReference: "REFUND-REF-1"}}
	repo := &fakeRepo{byID: map[domain.PaymentID]*domain.Payment{}}
	for _, p := range seed {
		repo.byID[p.ID()] = p
	}
	return &refundDeps{
		repo:   repo,
		psp:    psp,
		router: &fakeRouter{psp: psp},
		tx:     &fakeTx{},
		pub:    &fakePublisher{},
	}
}

func (d *refundDeps) uc() *RefundPaymentUseCase {
	return NewRefundPaymentUseCase(d.repo, d.router, d.tx, d.pub)
}

func TestRefundPaymentUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("refunds captured payment and publishes refunded event", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)

		res, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := RefundPaymentResult{PaymentID: p.ID().String(), PSPReference: "REFUND-REF-1", Status: "refunded"}
		if res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
		if p.Status() != domain.StatusRefunded {
			t.Errorf("status = %q, want refunded", p.Status())
		}
		if !reflect.DeepEqual(d.repo.updates, []domain.PaymentStatus{domain.StatusRefunded}) {
			t.Errorf("updates = %v", d.repo.updates)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.refunded.v1"}) {
			t.Errorf("events = %v", got)
		}
		ev := d.pub.events[0].(domain.PaymentRefundedEvent)
		if ev.PSPReference != "REFUND-REF-1" || ev.RefundAmount != 10_000 {
			t.Errorf("event = %+v", ev)
		}
	})

	t.Run("psp refund request is built from payment", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)

		if _, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := PSPRefundRequest{
			OriginalPSPReference: "PSP-REF-1",
			IdempotencyKey:       "refund_idem-1",
			Amount:               10_000,
			Currency:             "ARS",
		}
		if len(d.psp.refunds) != 1 || d.psp.refunds[0] != want {
			t.Errorf("refund requests = %+v, want [%+v]", d.psp.refunds, want)
		}
		if d.router.lastMethod != domain.MethodCard || d.router.lastTenant != domain.TenantID(tenantA) {
			t.Errorf("routed with %q / %q", d.router.lastMethod, d.router.lastTenant)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		d := newRefundDeps()
		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: "x", PaymentID: domain.NewPaymentID().String()})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
	})

	t.Run("invalid payment id", func(t *testing.T) {
		d := newRefundDeps()
		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid payment id") {
			t.Fatalf("expected invalid payment id error, got %v", err)
		}
	})

	t.Run("unknown payment is wrapped ErrPaymentNotFound", func(t *testing.T) {
		d := newRefundDeps()
		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: domain.NewPaymentID().String()})
		if !errors.Is(err, domain.ErrPaymentNotFound) || !strings.Contains(err.Error(), "find payment") {
			t.Fatalf("expected wrapped ErrPaymentNotFound, got %v", err)
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		d := newRefundDeps()
		d.repo.findErr = errBoom
		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: domain.NewPaymentID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("payment of another tenant is reported as not found", func(t *testing.T) {
		p := buildPayment(tenantB, domain.StatusCaptured)
		d := newRefundDeps(p)

		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, domain.ErrPaymentNotFound) {
			t.Fatalf("expected ErrPaymentNotFound, got %v", err)
		}
		if d.router.calls != 0 || len(d.psp.refunds) != 0 {
			t.Error("PSP must not be called for another tenant's payment")
		}
	})

	notRefundable := []domain.PaymentStatus{
		domain.StatusInitiated, domain.StatusProcessing, domain.StatusFailed,
		domain.StatusRiskRejected, domain.StatusRefunded,
	}
	for _, st := range notRefundable {
		t.Run("rejects payment in status "+st.String(), func(t *testing.T) {
			p := buildPayment(tenantA, st)
			d := newRefundDeps(p)

			_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
			if !errors.Is(err, domain.ErrNotCaptured) {
				t.Fatalf("expected ErrNotCaptured, got %v", err)
			}
			if len(d.psp.refunds) != 0 {
				t.Error("PSP must not be called")
			}
		})
	}

	t.Run("routing error is wrapped", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)
		d.router.err = errBoom

		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "route psp") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})

	t.Run("psp refund error leaves payment captured", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)
		d.psp.refundErr = errBoom

		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "psp refund") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if p.Status() != domain.StatusCaptured || len(d.repo.updates) != 0 || d.pub.calls != 0 {
			t.Error("payment must stay captured and nothing persisted")
		}
	})

	t.Run("update error is wrapped", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)
		d.repo.updateErr = errBoom

		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update refunded") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("event must not be published when update fails")
		}
	})

	t.Run("publish error is returned", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		d := newRefundDeps(p)
		d.pub.err = errBoom

		_, err := d.uc().Execute(ctx, RefundPaymentCmd{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestGetPaymentUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("returns full view of tenant's payment", func(t *testing.T) {
		p := buildPayment(tenantA, domain.StatusCaptured)
		repo := &fakeRepo{byID: map[domain.PaymentID]*domain.Payment{p.ID(): p}}

		v, err := NewGetPaymentUseCase(repo).Execute(ctx, GetPaymentQuery{TenantID: tenantA, PaymentID: p.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := PaymentView{
			ID:             p.ID().String(),
			TenantID:       tenantA,
			Status:         "captured",
			Amount:         10_000,
			Currency:       "ARS",
			PlatformFee:    300,
			PSPFee:         150,
			NetAmount:      9_550,
			PaymentMethod:  "card",
			PSPName:        "fakepsp",
			PSPReference:   "PSP-REF-1",
			IdempotencyKey: "idem-1",
			CapturedAt:     p.CapturedAt(),
			CreatedAt:      "2026-10-01T15:30:00Z",
		}
		if !reflect.DeepEqual(v, want) {
			t.Errorf("view = %+v\nwant %+v", v, want)
		}
	})

	t.Run("payment of another tenant is reported as not found", func(t *testing.T) {
		p := buildPayment(tenantB, domain.StatusCaptured)
		repo := &fakeRepo{byID: map[domain.PaymentID]*domain.Payment{p.ID(): p}}

		_, err := NewGetPaymentUseCase(repo).Execute(ctx, GetPaymentQuery{TenantID: tenantA, PaymentID: p.ID().String()})
		if !errors.Is(err, domain.ErrPaymentNotFound) {
			t.Fatalf("expected ErrPaymentNotFound, got %v", err)
		}
	})

	t.Run("repo error is propagated", func(t *testing.T) {
		repo := &fakeRepo{findErr: errBoom}
		_, err := NewGetPaymentUseCase(repo).Execute(ctx, GetPaymentQuery{TenantID: tenantA, PaymentID: domain.NewPaymentID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := NewGetPaymentUseCase(&fakeRepo{}).Execute(ctx, GetPaymentQuery{TenantID: "x", PaymentID: domain.NewPaymentID().String()})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
	})

	t.Run("invalid payment id", func(t *testing.T) {
		_, err := NewGetPaymentUseCase(&fakeRepo{}).Execute(ctx, GetPaymentQuery{TenantID: tenantA, PaymentID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid payment id") {
			t.Fatalf("expected invalid payment id error, got %v", err)
		}
	})
}
