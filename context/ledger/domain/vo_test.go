package domain

import (
	"errors"
	"testing"
)

func TestParseIDs(t *testing.T) {
	acc := NewAccountID().String()
	if _, err := ParseAccountID(acc); err != nil {
		t.Errorf("valid account id rejected: %v", err)
	}
	if _, err := ParseAccountID("nope"); err == nil {
		t.Error("expected error for invalid account id")
	}

	ent := NewEntryID().String()
	if _, err := ParseEntryID(ent); err != nil {
		t.Errorf("valid entry id rejected: %v", err)
	}
	if _, err := ParseEntryID("nope"); err == nil {
		t.Error("expected error for invalid entry id")
	}

	if _, err := ParseTenantID(NewAccountID().String()); err != nil {
		t.Errorf("valid tenant id rejected: %v", err)
	}
	if _, err := ParseTenantID("nope"); err == nil {
		t.Error("expected error for invalid tenant id")
	}
}

func TestParseAccountType(t *testing.T) {
	valid := []AccountType{
		AccountTypeMerchantBalance, AccountTypePlatformFees, AccountTypeReserve,
		AccountTypeInTransit, AccountTypeDisputeHold, AccountTypePayoutTransit, AccountTypePayoutSent,
	}
	for _, at := range valid {
		got, err := ParseAccountType(at.String())
		if err != nil || got != at {
			t.Errorf("ParseAccountType(%q) = %v, %v", at, got, err)
		}
	}
	if _, err := ParseAccountType("chicken"); !errors.Is(err, ErrInvalidAccountType) {
		t.Errorf("expected ErrInvalidAccountType, got %v", err)
	}
}

func TestParseDirectionAndOpposite(t *testing.T) {
	d, err := ParseDirection("debit")
	if err != nil || d != DirectionDebit {
		t.Fatalf("parse debit: %v %v", d, err)
	}
	c, err := ParseDirection("credit")
	if err != nil || c != DirectionCredit {
		t.Fatalf("parse credit: %v %v", c, err)
	}
	if _, err := ParseDirection("sideways"); !errors.Is(err, ErrInvalidDirection) {
		t.Errorf("expected ErrInvalidDirection, got %v", err)
	}

	if DirectionDebit.Opposite() != DirectionCredit {
		t.Error("debit opposite should be credit")
	}
	if DirectionCredit.Opposite() != DirectionDebit {
		t.Error("credit opposite should be debit")
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
		{"normalizes lowercase", 50, "usd", "USD", nil},
		{"trims and upcases", 50, " eur ", "EUR", nil},
		{"zero allowed", 0, "ARS", "ARS", nil},
		{"negative rejected", -1, "ARS", "", ErrNegativeAmount},
		{"too short currency", 100, "AR", "", ErrInvalidCurrency},
		{"too long currency", 100, "EURO", "", ErrInvalidCurrency},
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

func TestMoney_AddAndEqual(t *testing.T) {
	a := MustMoney(100, "ARS")
	b := MustMoney(50, "ARS")

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !sum.Equal(MustMoney(150, "ARS")) {
		t.Errorf("sum = %s, want 150 ARS", sum)
	}

	if _, err := a.Add(MustMoney(10, "USD")); !errors.Is(err, ErrInvalidMoneyOp) {
		t.Errorf("expected ErrInvalidMoneyOp on currency mismatch, got %v", err)
	}

	if a.Equal(MustMoney(100, "USD")) {
		t.Error("same amount different currency should not be equal")
	}
	if a.Equal(b) {
		t.Error("different amounts should not be equal")
	}
}

func TestMoney_IsZeroAndString(t *testing.T) {
	if !MustMoney(0, "ARS").IsZero() {
		t.Error("zero money should be zero")
	}
	if MustMoney(1, "ARS").IsZero() {
		t.Error("non-zero money should not be zero")
	}
	if got := MustMoney(2500, "USD").String(); got != "2500 USD" {
		t.Errorf("String() = %q, want '2500 USD'", got)
	}
}

func TestMustMoney_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustMoney should panic on invalid currency")
		}
	}()
	MustMoney(100, "BADCUR")
}
