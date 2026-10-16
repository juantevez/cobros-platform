package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewPaymentID(t *testing.T) {
	a, b := NewPaymentID(), NewPaymentID()
	if _, err := uuid.Parse(a.String()); err != nil {
		t.Fatalf("NewPaymentID is not a uuid: %v", err)
	}
	if a == b {
		t.Error("NewPaymentID should generate unique ids")
	}
}

func TestParsePaymentID(t *testing.T) {
	valid := "11111111-1111-1111-1111-111111111111"

	id, err := ParsePaymentID(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.String() != valid {
		t.Errorf("String() = %q, want %q", id, valid)
	}

	for _, in := range []string{"", "abc", "11111111-1111-1111-1111"} {
		if _, err := ParsePaymentID(in); err == nil {
			t.Errorf("ParsePaymentID(%q) expected error", in)
		}
	}
}

func TestParseTenantID(t *testing.T) {
	valid := "22222222-2222-2222-2222-222222222222"

	id, err := ParseTenantID(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.String() != valid {
		t.Errorf("String() = %q, want %q", id, valid)
	}

	for _, in := range []string{"", "tenant-1", "not-a-uuid"} {
		if _, err := ParseTenantID(in); err == nil {
			t.Errorf("ParseTenantID(%q) expected error", in)
		}
	}
}

func TestPaymentStatus(t *testing.T) {
	cases := []struct {
		status PaymentStatus
		str    string
		final  bool
	}{
		{StatusInitiated, "initiated", false},
		{StatusProcessing, "processing", false},
		{StatusCaptured, "captured", true},
		{StatusRiskRejected, "risk_rejected", true},
		{StatusFailed, "failed", true},
		{StatusRefunded, "refunded", true},
	}
	for _, tc := range cases {
		t.Run(tc.str, func(t *testing.T) {
			if tc.status.String() != tc.str {
				t.Errorf("String() = %q, want %q", tc.status.String(), tc.str)
			}
			if tc.status.IsFinal() != tc.final {
				t.Errorf("IsFinal() = %v, want %v", tc.status.IsFinal(), tc.final)
			}
		})
	}
}

func TestParsePaymentMethod(t *testing.T) {
	for _, m := range []PaymentMethod{MethodCard, MethodWallet, MethodTransfer, MethodQR} {
		got, err := ParsePaymentMethod(m.String())
		if err != nil {
			t.Errorf("ParsePaymentMethod(%q) unexpected error: %v", m, err)
		}
		if got != m {
			t.Errorf("ParsePaymentMethod(%q) = %q", m, got)
		}
	}

	// Case-sensitive: sólo los valores exactos son válidos.
	for _, in := range []string{"", "cash", "CARD", " card"} {
		if _, err := ParsePaymentMethod(in); !errors.Is(err, ErrInvalidMethod) {
			t.Errorf("ParsePaymentMethod(%q) expected ErrInvalidMethod, got %v", in, err)
		}
	}
}

func TestNewMoney(t *testing.T) {
	t.Run("valid money normalizes currency", func(t *testing.T) {
		m, err := NewMoney(1500, " ars ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Amount() != 1500 || m.Currency() != "ARS" {
			t.Errorf("got %d %q, want 1500 ARS", m.Amount(), m.Currency())
		}
		if m.String() != "1500 ARS" {
			t.Errorf("String() = %q", m.String())
		}
		if m.IsZero() {
			t.Error("IsZero() should be false")
		}
	})

	for _, amount := range []int64{0, -1} {
		t.Run("non-positive amount rejected", func(t *testing.T) {
			if _, err := NewMoney(amount, "ARS"); !errors.Is(err, ErrInvalidAmount) {
				t.Fatalf("NewMoney(%d) expected ErrInvalidAmount, got %v", amount, err)
			}
		})
	}

	for _, cur := range []string{"", "AR", "ARSS", "   "} {
		t.Run("invalid currency "+cur, func(t *testing.T) {
			if _, err := NewMoney(100, cur); !errors.Is(err, ErrInvalidCurrency) {
				t.Fatalf("NewMoney(%q) expected ErrInvalidCurrency, got %v", cur, err)
			}
		})
	}
}

func TestReconstituteMoney(t *testing.T) {
	// No valida: reconstruye lo que haya en la base tal cual.
	m := ReconstituteMoney(0, "usd")
	if m.Amount() != 0 || m.Currency() != "usd" || !m.IsZero() {
		t.Errorf("got %d %q zero=%v", m.Amount(), m.Currency(), m.IsZero())
	}
}

func TestMoney_Sub(t *testing.T) {
	ars := func(n int64) Money { return ReconstituteMoney(n, "ARS") }

	t.Run("subtracts same currency", func(t *testing.T) {
		got, err := ars(1000).Sub(ars(300))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != ars(700) {
			t.Errorf("got %v, want 700 ARS", got)
		}
	})

	t.Run("equal amounts give zero", func(t *testing.T) {
		got, err := ars(500).Sub(ars(500))
		if err != nil || !got.IsZero() || got.Currency() != "ARS" {
			t.Errorf("got %v, %v", got, err)
		}
	})

	t.Run("exceeding amount rejected", func(t *testing.T) {
		if _, err := ars(100).Sub(ars(101)); !errors.Is(err, ErrRefundExceedsAmount) {
			t.Fatalf("expected ErrRefundExceedsAmount, got %v", err)
		}
	})

	t.Run("different currencies rejected", func(t *testing.T) {
		_, err := ars(100).Sub(ReconstituteMoney(10, "USD"))
		if err == nil {
			t.Fatal("expected currency mismatch error")
		}
		if errors.Is(err, ErrRefundExceedsAmount) {
			t.Error("currency mismatch should not be ErrRefundExceedsAmount")
		}
	})
}
