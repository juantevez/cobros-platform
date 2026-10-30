package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewIDs(t *testing.T) {
	r1, r2 := NewRunID(), NewRunID()
	if _, err := uuid.Parse(r1.String()); err != nil || r1 == r2 {
		t.Errorf("NewRunID should return unique uuids: %q %q (%v)", r1, r2, err)
	}
	d1, d2 := NewDiscrepancyID(), NewDiscrepancyID()
	if _, err := uuid.Parse(d1.String()); err != nil || d1 == d2 {
		t.Errorf("NewDiscrepancyID should return unique uuids: %q %q (%v)", d1, d2, err)
	}
}

func TestParseIDs(t *testing.T) {
	valid := "11111111-1111-1111-1111-111111111111"
	invalid := []string{"", "abc", "11111111-1111-1111-1111"}

	parsers := []struct {
		name    string
		parse   func(string) (string, error)
		errText string
	}{
		{"run", func(s string) (string, error) { id, err := ParseRunID(s); return id.String(), err }, "invalid run id"},
		{"discrepancy", func(s string) (string, error) { id, err := ParseDiscrepancyID(s); return id.String(), err }, "invalid discrepancy id"},
		{"tenant", func(s string) (string, error) { id, err := ParseTenantID(s); return id.String(), err }, "invalid tenant id"},
	}
	for _, p := range parsers {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(valid)
			if err != nil || got != valid {
				t.Fatalf("parse(valid) = %q, %v", got, err)
			}
			for _, in := range invalid {
				got, err := p.parse(in)
				if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), p.errText) {
					t.Errorf("parse(%q) expected %q error, got %v", in, p.errText, err)
				}
				if got != "" {
					t.Errorf("parse(%q) should return empty id, got %q", in, got)
				}
			}
		})
	}
}

func TestParseReconciliationType(t *testing.T) {
	for _, want := range []ReconciliationType{TypePayment, TypeInternal} {
		got, err := ParseReconciliationType(want.String())
		if err != nil || got != want {
			t.Errorf("ParseReconciliationType(%q) = %q, %v", want, got, err)
		}
	}
	for _, in := range []string{"", "bank", "PAYMENT", "internal"} {
		if _, err := ParseReconciliationType(in); err == nil {
			t.Errorf("ParseReconciliationType(%q) expected error", in)
		}
	}
}

func TestEnumStrings(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{TypePayment.String(), "payment"},
		{TypeInternal.String(), "internal_ledger"},
		{RunStatusPending.String(), "pending"},
		{RunStatusRunning.String(), "running"},
		{RunStatusCompleted.String(), "completed"},
		{RunStatusFailed.String(), "failed"},
		{DiscrepancyMissingInPSP.String(), "missing_in_psp"},
		{DiscrepancyMissingInSystem.String(), "missing_in_system"},
		{DiscrepancyAmountMismatch.String(), "amount_mismatch"},
		{DiscrepancyStatusMismatch.String(), "status_mismatch"},
		{DiscrepancyLedgerImbalance.String(), "ledger_imbalance"},
		{DiscrepancyOpen.String(), "open"},
		{DiscrepancyResolved.String(), "resolved"},
		{DiscrepancyIgnored.String(), "ignored"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("String() = %q, want %q", tc.got, tc.want)
		}
	}
}
