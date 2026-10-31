package psp

import (
	"context"
	"testing"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// namedPSP es un adaptador mínimo identificable por nombre.
type namedPSP struct{ name string }

func (p namedPSP) AuthorizeAndCapture(context.Context, application.PSPCaptureRequest) (application.PSPCaptureResult, error) {
	return application.PSPCaptureResult{}, nil
}
func (p namedPSP) Refund(context.Context, application.PSPRefundRequest) (application.PSPRefundResult, error) {
	return application.PSPRefundResult{}, nil
}
func (p namedPSP) GetCapture(context.Context, application.PSPCaptureLookupRequest) (application.PSPCaptureLookup, error) {
	return application.PSPCaptureLookup{}, nil
}
func (p namedPSP) Name() string { return p.name }

func TestRouter_Route(t *testing.T) {
	t.Run("returns mock adapter for every method", func(t *testing.T) {
		r := NewRouter()
		for _, m := range []domain.PaymentMethod{domain.MethodCard, domain.MethodWallet, domain.MethodTransfer, domain.MethodQR} {
			a, err := r.Route(context.Background(), m, "tenant")
			if err != nil {
				t.Fatalf("Route(%q) unexpected error: %v", m, err)
			}
			if a.Name() != "mock" {
				t.Errorf("Route(%q) = %q, want mock", m, a.Name())
			}
		}
	})

	t.Run("registering another PSP does not change routing", func(t *testing.T) {
		r := NewRouter()
		r.Register(namedPSP{"mercadopago"})

		a, _ := r.Route(context.Background(), domain.MethodWallet, "tenant")
		if a.Name() != "mock" {
			t.Errorf("Route = %q, want mock", a.Name())
		}
		if _, ok := r.adapters["mercadopago"]; !ok {
			t.Error("registered adapter not stored")
		}
	})

	t.Run("register replaces adapter with same name", func(t *testing.T) {
		r := NewRouter()
		replacement := namedPSP{"mock"}
		r.Register(replacement)

		a, _ := r.Route(context.Background(), domain.MethodCard, "tenant")
		if a != replacement {
			t.Errorf("Route = %#v, want replacement adapter", a)
		}
	})

	t.Run("error when mock adapter is missing", func(t *testing.T) {
		r := &Router{adapters: map[string]application.PSPAdapter{}}
		if _, err := r.Route(context.Background(), domain.MethodCard, "tenant"); err == nil {
			t.Fatal("expected error with no adapters")
		}
	})
}
