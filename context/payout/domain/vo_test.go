package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewPayoutID(t *testing.T) {
	a, b := NewPayoutID(), NewPayoutID()
	if _, err := uuid.Parse(a.String()); err != nil {
		t.Fatalf("NewPayoutID is not a uuid: %v", err)
	}
	if a == b {
		t.Error("NewPayoutID should generate unique ids")
	}
}

func TestParsePayoutID(t *testing.T) {
	valid := "11111111-1111-1111-1111-111111111111"

	id, err := ParsePayoutID(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.String() != valid {
		t.Errorf("String() = %q, want %q", id, valid)
	}

	for _, in := range []string{"", "abc", "11111111-1111-1111-1111"} {
		if _, err := ParsePayoutID(in); err == nil {
			t.Errorf("ParsePayoutID(%q) expected error", in)
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

func TestPayoutStatus(t *testing.T) {
	cases := []struct {
		status PayoutStatus
		str    string
		final  bool
	}{
		{StatusInitiated, "initiated", false},
		{StatusProcessing, "processing", false},
		{StatusConfirmed, "confirmed", true},
		{StatusFailed, "failed", true},
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

	t.Run("amount validated before currency", func(t *testing.T) {
		if _, err := NewMoney(0, "X"); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("expected ErrInvalidAmount, got %v", err)
		}
	})
}

func TestReconstituteMoney(t *testing.T) {
	// No valida: reconstruye lo que haya en la base tal cual.
	m := ReconstituteMoney(0, "usd")
	if m.Amount() != 0 || m.Currency() != "usd" {
		t.Errorf("got %d %q, want 0 usd", m.Amount(), m.Currency())
	}
}
