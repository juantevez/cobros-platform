package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseGranularity(t *testing.T) {
	valid := []struct {
		in   string
		want Granularity
	}{
		{"", GranularityDay}, // vacío = default day
		{"day", GranularityDay},
		{"week", GranularityWeek},
		{"month", GranularityMonth},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.in, func(t *testing.T) {
			got, err := ParseGranularity(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("ParseGranularity(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}

	for _, in := range []string{"year", "hour", "DAY", " week", "days"} {
		t.Run("invalid "+in, func(t *testing.T) {
			got, err := ParseGranularity(in)
			if err == nil || got != "" {
				t.Fatalf("ParseGranularity(%q) = %q, %v; want error", in, got, err)
			}
			if !strings.Contains(err.Error(), "use day|week|month") {
				t.Errorf("error should list valid options: %v", err)
			}
		})
	}
}

func TestGranularity_String(t *testing.T) {
	for g, want := range map[Granularity]string{
		GranularityDay:   "day",
		GranularityWeek:  "week",
		GranularityMonth: "month",
	} {
		if g.String() != want {
			t.Errorf("String() = %q, want %q", g.String(), want)
		}
	}
}

// Las vistas son el contrato JSON del dashboard: los nombres de campo no
// pueden cambiar sin romper a los clientes.
func TestViews_JSONContract(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{
			"VolumePoint",
			VolumePoint{Bucket: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Currency: "ARS", PaymentCount: 3, GrossAmount: 900},
			`{"bucket":"2026-10-01T00:00:00Z","currency":"ARS","payment_count":3,"gross_amount":900}`,
		},
		{
			"RevenueSummary",
			RevenueSummary{Currency: "ARS", PaymentCount: 2, GrossAmount: 1000, PlatformFees: 30, PSPFees: 15},
			`{"currency":"ARS","payment_count":2,"gross_amount":1000,"platform_fees":30,"psp_fees":15}`,
		},
		{
			"TenantBalance",
			TenantBalance{AccountType: "merchant_balance", Currency: "ARS", Debits: 100, Credits: 400, Net: -300},
			`{"account_type":"merchant_balance","currency":"ARS","debits":100,"credits":400,"net":-300}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("json = %s\nwant %s", b, tc.want)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	if errors.Is(ErrInvalidFact, ErrInvalidRange) {
		t.Error("errors must be distinct")
	}
	for _, err := range []error{ErrInvalidFact, ErrInvalidRange} {
		if !strings.HasPrefix(err.Error(), "reporting: ") {
			t.Errorf("error %q should be prefixed with the context name", err)
		}
	}
}
