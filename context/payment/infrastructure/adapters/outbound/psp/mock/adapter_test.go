package mock

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payment/application"
)

func TestAdapter_Name(t *testing.T) {
	if got := New().Name(); got != "mock" {
		t.Errorf("Name() = %q, want mock", got)
	}
}

func TestAdapter_AuthorizeAndCapture(t *testing.T) {
	t.Run("approves with 1.5% psp fee and unique reference", func(t *testing.T) {
		a := New()
		req := application.PSPCaptureRequest{Amount: 10_000, PaymentToken: "tok"}

		r1, err := a.AuthorizeAndCapture(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r2, _ := a.AuthorizeAndCapture(context.Background(), req)

		if r1.Status != "approved" || r1.PSPFee != 150 {
			t.Errorf("result = %+v, want approved with fee 150", r1)
		}
		if !strings.HasPrefix(r1.PSPReference, "MOCK-") || len(r1.PSPReference) != len("MOCK-")+8 {
			t.Errorf("unexpected reference %q", r1.PSPReference)
		}
		if r1.PSPReference == r2.PSPReference {
			t.Error("references should be unique")
		}
	})

	t.Run("small amounts have minimum fee of 1", func(t *testing.T) {
		r, err := New().AuthorizeAndCapture(context.Background(), application.PSPCaptureRequest{Amount: 10})
		if err != nil || r.PSPFee != 1 {
			t.Fatalf("got fee %d, %v; want 1", r.PSPFee, err)
		}
	})

	t.Run("REJECT token is a definitive decline", func(t *testing.T) {
		_, err := New().AuthorizeAndCapture(context.Background(), application.PSPCaptureRequest{Amount: 100, PaymentToken: "REJECT"})
		if !errors.Is(err, application.ErrPSPDeclined) || !strings.Contains(err.Error(), "payment rejected") {
			t.Fatalf("expected ErrPSPDeclined, got %v", err)
		}
	})

	t.Run("TIMEOUT token is an unknown outcome", func(t *testing.T) {
		_, err := New().AuthorizeAndCapture(context.Background(), application.PSPCaptureRequest{Amount: 100, PaymentToken: "TIMEOUT"})
		if err == nil || errors.Is(err, application.ErrPSPDeclined) {
			t.Fatalf("expected a non-decline error, got %v", err)
		}
	})
}

func TestAdapter_Refund(t *testing.T) {
	a := New()
	r1, err := a.Refund(context.Background(), application.PSPRefundRequest{OriginalPSPReference: "MOCK-1", Amount: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, _ := a.Refund(context.Background(), application.PSPRefundRequest{})

	if !strings.HasPrefix(r1.PSPReference, "MOCK-REF-") || len(r1.PSPReference) != len("MOCK-REF-")+8 {
		t.Errorf("unexpected reference %q", r1.PSPReference)
	}
	if r1.PSPReference == r2.PSPReference {
		t.Error("references should be unique")
	}
}
