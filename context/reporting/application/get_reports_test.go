package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

var (
	from = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// rangeCases cubre la validación de rango compartida por Volume y Revenue.
var rangeCases = []struct {
	name     string
	from, to time.Time
	wantErr  error
}{
	{"valid range", from, to, nil},
	{"same instant", from, from, nil},
	{"no range", time.Time{}, time.Time{}, nil},
	{"only from (no validation)", to, time.Time{}, nil},
	{"only to (no validation)", time.Time{}, from, nil},
	{"inverted range", to, from, domain.ErrInvalidRange},
}

func TestGetReportsUseCase_GetVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates query to reader", func(t *testing.T) {
		points := []domain.VolumePoint{{Bucket: from, Currency: "ARS", PaymentCount: 3, GrossAmount: 900}}
		r := &fakeReader{volume: points}
		q := VolumeQuery{TenantID: tenantA, From: from, To: to, Granularity: domain.GranularityWeek}

		got, err := NewGetReportsUseCase(r).GetVolume(ctx, q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, points) {
			t.Errorf("got %+v, want %+v", got, points)
		}
		if r.lastVolume != q {
			t.Errorf("reader got %+v, want %+v", r.lastVolume, q)
		}
	})

	for _, tc := range rangeCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeReader{}
			_, err := NewGetReportsUseCase(r).GetVolume(ctx, VolumeQuery{TenantID: tenantA, From: tc.from, To: tc.to})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			wantCalls := 1
			if tc.wantErr != nil {
				wantCalls = 0
			}
			if r.volumeCalls != wantCalls {
				t.Errorf("reader calls = %d, want %d", r.volumeCalls, wantCalls)
			}
		})
	}

	t.Run("reader error is returned", func(t *testing.T) {
		_, err := NewGetReportsUseCase(&fakeReader{err: errBoom}).GetVolume(ctx, VolumeQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestGetReportsUseCase_GetRevenue(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates query to reader", func(t *testing.T) {
		summary := []domain.RevenueSummary{{Currency: "ARS", PaymentCount: 2, GrossAmount: 1000, PlatformFees: 30, PSPFees: 15}}
		r := &fakeReader{revenue: summary}
		q := RevenueQuery{TenantID: tenantA, From: from, To: to}

		got, err := NewGetReportsUseCase(r).GetRevenue(ctx, q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, summary) {
			t.Errorf("got %+v, want %+v", got, summary)
		}
		if r.lastRevenue != q {
			t.Errorf("reader got %+v, want %+v", r.lastRevenue, q)
		}
	})

	for _, tc := range rangeCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeReader{}
			_, err := NewGetReportsUseCase(r).GetRevenue(ctx, RevenueQuery{TenantID: tenantA, From: tc.from, To: tc.to})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && r.revenueCalls != 0 {
				t.Error("reader should not be called on invalid range")
			}
		})
	}

	t.Run("reader error is returned", func(t *testing.T) {
		_, err := NewGetReportsUseCase(&fakeReader{err: errBoom}).GetRevenue(ctx, RevenueQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestGetReportsUseCase_GetBalances(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates tenant to reader", func(t *testing.T) {
		balances := []domain.TenantBalance{{AccountType: "merchant_balance", Currency: "ARS", Debits: 100, Credits: 400, Net: -300}}
		r := &fakeReader{balances: balances}

		got, err := NewGetReportsUseCase(r).GetBalances(ctx, tenantA)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, balances) || r.lastTenant != tenantA {
			t.Errorf("got %+v (tenant %q)", got, r.lastTenant)
		}
	})

	t.Run("reader error is returned", func(t *testing.T) {
		_, err := NewGetReportsUseCase(&fakeReader{err: errBoom}).GetBalances(ctx, tenantA)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
