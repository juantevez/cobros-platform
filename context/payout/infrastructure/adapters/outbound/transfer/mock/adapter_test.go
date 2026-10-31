package mock

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payout/application"
)

func TestAdapter_Name(t *testing.T) {
	if got := New().Name(); got != "mock_bank" {
		t.Errorf("Name() = %q, want mock_bank", got)
	}
}

func TestAdapter_Transfer(t *testing.T) {
	t.Run("confirms with unique mock reference", func(t *testing.T) {
		a := New()
		req := application.TransferRequest{PayoutID: "p1", Amount: 100, AccountNumber: "123"}

		r1, err := a.Transfer(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r2, _ := a.Transfer(context.Background(), req)

		if r1.Status != "confirmed" {
			t.Errorf("status = %q, want confirmed", r1.Status)
		}
		if !strings.HasPrefix(r1.BankReference, "MOCK-BANK-") || len(r1.BankReference) != len("MOCK-BANK-")+8 {
			t.Errorf("unexpected reference %q", r1.BankReference)
		}
		if r1.BankReference == r2.BankReference {
			t.Error("references should be unique per transfer")
		}
	})

	t.Run("REJECT account is a definitive rejection", func(t *testing.T) {
		_, err := New().Transfer(context.Background(), application.TransferRequest{AccountNumber: "REJECT"})
		if !errors.Is(err, application.ErrBankRejected) || !strings.Contains(err.Error(), "account rejected") {
			t.Fatalf("expected ErrBankRejected, got %v", err)
		}
	})

	t.Run("TIMEOUT account is an unknown outcome", func(t *testing.T) {
		_, err := New().Transfer(context.Background(), application.TransferRequest{AccountNumber: "TIMEOUT"})
		if err == nil || errors.Is(err, application.ErrBankRejected) {
			t.Fatalf("expected a non-rejection error, got %v", err)
		}
	})
}
