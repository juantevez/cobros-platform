package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

const tenantA = "11111111-1111-1111-1111-111111111111"

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeReader struct {
	volume   []domain.VolumePoint
	revenue  []domain.RevenueSummary
	balances []domain.TenantBalance
	err      error

	lastVolume  application.VolumeQuery
	lastRevenue application.RevenueQuery
	lastTenant  string
	calls       int
}

func (r *fakeReader) Volume(ctx context.Context, q application.VolumeQuery) ([]domain.VolumePoint, error) {
	r.calls++
	r.lastVolume = q
	return r.volume, r.err
}

func (r *fakeReader) Revenue(ctx context.Context, q application.RevenueQuery) ([]domain.RevenueSummary, error) {
	r.calls++
	r.lastRevenue = q
	return r.revenue, r.err
}

func (r *fakeReader) Balances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error) {
	r.calls++
	r.lastTenant = tenantID
	return r.balances, r.err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newHandler(r *fakeReader) *ReportingHandler {
	return NewReportingHandler(application.NewGetReportsUseCase(r))
}

func doRequest(h gin.HandlerFunc, target, tenantID string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if tenantID != "" {
		req = req.WithContext(postgres.WithTenantID(req.Context(), tenantID))
	}
	c.Request = req
	h(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

var (
	from = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// rangeErrors cubre el parseo de from/to compartido por volume y revenue.
var rangeErrors = []struct {
	name, query, wantErr string
}{
	{"non RFC3339 from", "from=2026-09-01", "invalid 'from': use RFC3339"},
	{"non RFC3339 to", "to=tomorrow", "invalid 'to': use RFC3339"},
	{"inverted range", "from=2026-10-01T00:00:00Z&to=2026-09-01T00:00:00Z", domain.ErrInvalidRange.Error()},
}

// ── Volume ────────────────────────────────────────────────────────────────────

func TestGetVolume(t *testing.T) {
	t.Run("returns points with tenant, range and granularity", func(t *testing.T) {
		r := &fakeReader{volume: []domain.VolumePoint{{Bucket: from, Currency: "ARS", PaymentCount: 3, GrossAmount: 900}}}

		rec := doRequest(newHandler(r).GetVolume, "/reports/volume?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z&granularity=month", tenantA)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		want := application.VolumeQuery{TenantID: tenantA, From: from, To: to, Granularity: domain.GranularityMonth}
		if r.lastVolume != want {
			t.Errorf("query = %+v\nwant %+v", r.lastVolume, want)
		}
		var body struct {
			Granularity string               `json:"granularity"`
			Points      []domain.VolumePoint `json:"points"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if body.Granularity != "month" || len(body.Points) != 1 || body.Points[0].GrossAmount != 900 {
			t.Errorf("body = %+v", body)
		}
	})

	t.Run("defaults to daily granularity and no range", func(t *testing.T) {
		r := &fakeReader{}
		rec := doRequest(newHandler(r).GetVolume, "/reports/volume", tenantA)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if r.lastVolume.Granularity != domain.GranularityDay || !r.lastVolume.From.IsZero() || !r.lastVolume.To.IsZero() {
			t.Errorf("query = %+v", r.lastVolume)
		}
		if !strings.Contains(rec.Body.String(), `"granularity":"day"`) {
			t.Errorf("body = %s", rec.Body)
		}
	})

	t.Run("invalid granularity returns 400", func(t *testing.T) {
		r := &fakeReader{}
		rec := doRequest(newHandler(r).GetVolume, "/reports/volume?granularity=year", tenantA)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(errorBody(t, rec), "invalid granularity") || r.calls != 0 {
			t.Errorf("error = %q, calls = %d", errorBody(t, rec), r.calls)
		}
	})

	for _, tc := range rangeErrors {
		t.Run("400 on "+tc.name, func(t *testing.T) {
			r := &fakeReader{}
			rec := doRequest(newHandler(r).GetVolume, "/reports/volume?"+tc.query, tenantA)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorBody(t, rec); got != tc.wantErr {
				t.Errorf("error = %q, want %q", got, tc.wantErr)
			}
			if r.calls != 0 {
				t.Error("reader should not be called")
			}
		})
	}

	t.Run("reader error returns 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeReader{err: errBoom}).GetVolume, "/reports/volume", tenantA)
		if rec.Code != http.StatusInternalServerError || errorBody(t, rec) != "internal server error" {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
		}
	})
}

// ── Revenue ───────────────────────────────────────────────────────────────────

func TestGetRevenue(t *testing.T) {
	t.Run("returns revenue summary", func(t *testing.T) {
		r := &fakeReader{revenue: []domain.RevenueSummary{{Currency: "ARS", PaymentCount: 2, PlatformFees: 30}}}

		rec := doRequest(newHandler(r).GetRevenue, "/reports/revenue?from=2026-09-01T00:00:00Z", tenantA)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		want := application.RevenueQuery{TenantID: tenantA, From: from}
		if r.lastRevenue != want {
			t.Errorf("query = %+v, want %+v", r.lastRevenue, want)
		}
		var body struct {
			Revenue []domain.RevenueSummary `json:"revenue"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Revenue) != 1 || body.Revenue[0].PlatformFees != 30 {
			t.Errorf("body = %s", rec.Body)
		}
	})

	for _, tc := range rangeErrors {
		t.Run("400 on "+tc.name, func(t *testing.T) {
			r := &fakeReader{}
			rec := doRequest(newHandler(r).GetRevenue, "/reports/revenue?"+tc.query, tenantA)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorBody(t, rec); got != tc.wantErr {
				t.Errorf("error = %q, want %q", got, tc.wantErr)
			}
		})
	}

	t.Run("reader error returns 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeReader{err: errBoom}).GetRevenue, "/reports/revenue", tenantA)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── Balances ──────────────────────────────────────────────────────────────────

func TestGetBalances(t *testing.T) {
	t.Run("returns balances of authenticated tenant", func(t *testing.T) {
		r := &fakeReader{balances: []domain.TenantBalance{{AccountType: "merchant_balance", Currency: "ARS", Net: -300}}}

		rec := doRequest(newHandler(r).GetBalances, "/reports/balances", tenantA)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if r.lastTenant != tenantA {
			t.Errorf("tenant = %q", r.lastTenant)
		}
		if !strings.Contains(rec.Body.String(), `"net":-300`) {
			t.Errorf("body = %s", rec.Body)
		}
	})

	t.Run("reader error returns 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeReader{err: errBoom}).GetBalances, "/reports/balances", tenantA)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── respondReportError / RegisterRoutes ───────────────────────────────────────

func TestRespondReportError(t *testing.T) {
	cases := []struct {
		err  error
		code int
		body string
	}{
		{domain.ErrInvalidRange, http.StatusBadRequest, domain.ErrInvalidRange.Error()},
		{fmt.Errorf("wrapped: %w", domain.ErrInvalidRange), http.StatusBadRequest, "wrapped: " + domain.ErrInvalidRange.Error()},
		{domain.ErrInvalidFact, http.StatusInternalServerError, "internal server error"},
		{errBoom, http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			respondReportError(c, tc.err)
			if rec.Code != tc.code || errorBody(t, rec) != tc.body {
				t.Errorf("got %d %q, want %d %q", rec.Code, errorBody(t, rec), tc.code, tc.body)
			}
		})
	}
}

func TestRegisterRoutes(t *testing.T) {
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), newHandler(&fakeReader{}))

	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)

	want := []string{
		"GET /api/v1/reports/balances",
		"GET /api/v1/reports/revenue",
		"GET /api/v1/reports/volume",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("routes = %v, want %v", got, want)
	}
}
