package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestParseIDs(t *testing.T) {
	if _, err := ParsePlanID(uuid.NewString()); err != nil {
		t.Errorf("valid plan id rejected: %v", err)
	}
	if _, err := ParsePlanID("nope"); err == nil {
		t.Error("expected error for invalid plan id")
	}
	if _, err := ParseTenantID(uuid.NewString()); err != nil {
		t.Errorf("valid tenant id rejected: %v", err)
	}
	if _, err := ParseTenantID("nope"); err == nil {
		t.Error("expected error for invalid tenant id")
	}
}

func TestIDConstructorsAndStrings(t *testing.T) {
	if NewFeeID().String() == "" {
		t.Error("NewFeeID should produce a non-empty id")
	}
	if NewTenantPlanID().String() == "" {
		t.Error("NewTenantPlanID should produce a non-empty id")
	}
	if TenantPlanID("tp-1").String() != "tp-1" || FeeID("f-1").String() != "f-1" {
		t.Error("id String() methods mismatch")
	}
}

func TestParsePaymentMethod(t *testing.T) {
	for _, m := range []PaymentMethod{MethodCard, MethodWallet, MethodTransfer, MethodQR} {
		if got, err := ParsePaymentMethod(m.String()); err != nil || got != m {
			t.Errorf("ParsePaymentMethod(%q) = %v, %v", m, got, err)
		}
	}
	if _, err := ParsePaymentMethod("bitcoin"); !errors.Is(err, ErrInvalidPaymentMethod) {
		t.Errorf("expected ErrInvalidPaymentMethod, got %v", err)
	}
}

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		wantCur  string
		wantErr  error
	}{
		{"valid", 100, "ARS", "ARS", nil},
		{"normalizes currency", 50, " usd ", "USD", nil},
		{"zero allowed", 0, "ARS", "ARS", nil},
		{"negative rejected", -1, "ARS", "", ErrInvalidFixedAmount},
		{"bad currency length", 100, "AR", "", ErrInvalidCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMoney(tt.amount, tt.currency)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Amount() != tt.amount || m.Currency() != tt.wantCur {
				t.Errorf("got %d %s, want %d %s", m.Amount(), m.Currency(), tt.amount, tt.wantCur)
			}
		})
	}
}

func TestMoney_Helpers(t *testing.T) {
	z := ZeroMoney("ars")
	if !z.IsZero() || z.Currency() != "ARS" {
		t.Errorf("ZeroMoney wrong: %+v", z)
	}
	r := ReconstituteMoney(2500, "USD")
	if r.Amount() != 2500 || r.Currency() != "USD" {
		t.Errorf("ReconstituteMoney wrong: %+v", r)
	}
	if r.IsZero() {
		t.Error("2500 should not be zero")
	}
	if got := ReconstituteMoney(300, "ARS").String(); got != "300 ARS" {
		t.Errorf("String() = %q, want '300 ARS'", got)
	}
}
