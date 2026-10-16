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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/payout/application"
	"github.com/juantevez/cobros-platform/context/payout/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeRepo struct {
	byID      map[domain.PayoutID]*domain.Payout
	listed    []*domain.Payout
	listErr   error
	lastLimit int
}

func newRepo() *fakeRepo { return &fakeRepo{byID: map[domain.PayoutID]*domain.Payout{}} }

func (r *fakeRepo) Save(ctx context.Context, p *domain.Payout) error {
	r.byID[p.ID()] = p
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, p *domain.Payout) error { return nil }
func (r *fakeRepo) FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPayoutNotFound
	}
	return p, nil
}
func (r *fakeRepo) ListByTenant(ctx context.Context, t domain.TenantID, limit int) ([]*domain.Payout, error) {
	r.lastLimit = limit
	return r.listed, r.listErr
}

type fakeBalance struct{ balance int64 }

func (b fakeBalance) GetAvailableBalance(ctx context.Context, t domain.TenantID, cur string) (int64, error) {
	return b.balance, nil
}

type fakeBank struct{ account domain.BankAccountInfo }

func (b fakeBank) GetBankAccount(ctx context.Context, t domain.TenantID) (domain.BankAccountInfo, error) {
	return b.account, nil
}

type fakeTransfer struct{ err error }

func (f fakeTransfer) Transfer(ctx context.Context, req application.TransferRequest) (application.TransferResult, error) {
	if f.err != nil {
		return application.TransferResult{}, f.err
	}
	return application.TransferResult{BankReference: "BANK-1", Status: "confirmed"}, nil
}
func (fakeTransfer) Name() string { return "fake" }

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

// ── Helpers ───────────────────────────────────────────────────────────────────

type handlerOpts struct {
	balance     int64
	account     domain.BankAccountInfo
	transferErr error
}

func defaultOpts() handlerOpts {
	return handlerOpts{
		balance: 10_000,
		account: domain.BankAccountInfo{AccountType: "CBU", AccountNumber: "0170099220000067797370", HolderName: "Comercio SA"},
	}
}

func newHandler(r *fakeRepo, o handlerOpts) *PayoutHandler {
	return NewPayoutHandler(
		application.NewInitiatePayoutUseCase(r, fakeBalance{o.balance}, fakeBank{o.account},
			fakeTransfer{o.transferErr}, fakeTx{}, fakePublisher{}),
		application.NewGetPayoutUseCase(r),
		application.NewListPayoutsUseCase(r),
	)
}

func doRequest(h gin.HandlerFunc, method, target, body, tenantID string, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if tenantID != "" {
		req = req.WithContext(postgres.WithTenantID(req.Context(), tenantID))
	}
	c.Request = req
	c.Params = params
	h(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func seedPayout(r *fakeRepo, tenantID string) *domain.Payout {
	money, _ := domain.NewMoney(5000, "ARS")
	p, _ := domain.NewPayout(domain.NewPayoutID(), domain.TenantID(tenantID), money, defaultOpts().account)
	p.PullEvents()
	r.byID[p.ID()] = p
	return p
}

func param(id string) gin.Params { return gin.Params{{Key: "payoutID", Value: id}} }

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

// ── Initiate ──────────────────────────────────────────────────────────────────

func TestInitiate(t *testing.T) {
	tid := uuid.NewString()

	t.Run("creates payout returns 201", func(t *testing.T) {
		r := newRepo()
		rec := doRequest(newHandler(r, defaultOpts()).Initiate, http.MethodPost, "/payouts",
			`{"amount":3000,"currency":"ARS"}`, tid, nil)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var res application.InitiatePayoutResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if res.PayoutID == "" || res.Amount != 3000 || res.Status != "confirmed" || res.BankReference != "BANK-1" {
			t.Errorf("unexpected result: %+v", res)
		}
		if len(r.byID) != 1 {
			t.Errorf("expected payout saved, got %d", len(r.byID))
		}
	})

	t.Run("empty body object uses full balance", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Initiate, http.MethodPost, "/payouts", `{}`, tid, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var res application.InitiatePayoutResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res.Amount != 10_000 || res.Currency != "ARS" {
			t.Errorf("unexpected result: %+v", res)
		}
	})

	t.Run("malformed json returns 400", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Initiate, http.MethodPost, "/payouts", `{bad`, tid, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := errorBody(t, rec); got != "invalid request body" {
			t.Errorf("error = %q", got)
		}
	})

	t.Run("amount over balance returns 422", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Initiate, http.MethodPost, "/payouts",
			`{"amount":10001}`, tid, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})

	t.Run("no bank account returns 422", func(t *testing.T) {
		o := defaultOpts()
		o.account = domain.BankAccountInfo{}
		rec := doRequest(newHandler(newRepo(), o).Initiate, http.MethodPost, "/payouts", `{}`, tid, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})

	t.Run("invalid currency returns 400", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Initiate, http.MethodPost, "/payouts",
			`{"amount":100,"currency":"PESOS"}`, tid, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("transfer failure returns 500 without leaking details", func(t *testing.T) {
		o := defaultOpts()
		o.transferErr = errors.New("bank secret detail")
		rec := doRequest(newHandler(newRepo(), o).Initiate, http.MethodPost, "/payouts", `{"amount":100}`, tid, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := errorBody(t, rec); got != "internal server error" {
			t.Errorf("error = %q", got)
		}
	})
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns payout view", func(t *testing.T) {
		r := newRepo()
		p := seedPayout(r, tid)

		rec := doRequest(newHandler(r, defaultOpts()).Get, http.MethodGet, "/payouts/"+p.ID().String(), "", tid, param(p.ID().String()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var v application.PayoutView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if v.ID != p.ID().String() || v.TenantID != tid || v.Status != "initiated" || v.Amount != 5000 {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("unknown payout returns 404", func(t *testing.T) {
		id := uuid.NewString()
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Get, http.MethodGet, "/payouts/"+id, "", tid, param(id))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("payout of another tenant returns 404", func(t *testing.T) {
		r := newRepo()
		p := seedPayout(r, uuid.NewString())

		rec := doRequest(newHandler(r, defaultOpts()).Get, http.MethodGet, "/payouts/x", "", tid, param(p.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestList(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns payouts with count", func(t *testing.T) {
		r := newRepo()
		r.listed = []*domain.Payout{seedPayout(r, tid), seedPayout(r, tid)}

		rec := doRequest(newHandler(r, defaultOpts()).List, http.MethodGet, "/payouts?limit=5", "", tid, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body struct {
			Payouts []application.PayoutView `json:"payouts"`
			Count   int                      `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if body.Count != 2 || len(body.Payouts) != 2 {
			t.Errorf("count=%d len=%d, want 2", body.Count, len(body.Payouts))
		}
		if r.lastLimit != 5 {
			t.Errorf("limit = %d, want 5", r.lastLimit)
		}
	})

	limits := []struct{ query, name string }{
		{"/payouts", "missing limit"},
		{"/payouts?limit=abc", "non-numeric limit"},
		{"/payouts?limit=-1", "negative limit"},
	}
	for _, tc := range limits {
		t.Run(tc.name+" falls back to 20", func(t *testing.T) {
			r := newRepo()
			rec := doRequest(newHandler(r, defaultOpts()).List, http.MethodGet, tc.query, "", tid, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if r.lastLimit != 20 {
				t.Errorf("limit = %d, want 20", r.lastLimit)
			}
		})
	}

	t.Run("empty list returns empty array", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).List, http.MethodGet, "/payouts", "", tid, nil)
		if !strings.Contains(rec.Body.String(), `"payouts":[]`) || !strings.Contains(rec.Body.String(), `"count":0`) {
			t.Errorf("body = %s", rec.Body)
		}
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		r := newRepo()
		r.listErr = errBoom
		rec := doRequest(newHandler(r, defaultOpts()).List, http.MethodGet, "/payouts", "", tid, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── respondDomainError ────────────────────────────────────────────────────────

func TestRespondDomainError(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{domain.ErrPayoutNotFound, http.StatusNotFound},
		{domain.ErrInsufficientBalance, http.StatusUnprocessableEntity},
		{domain.ErrNoBankAccount, http.StatusUnprocessableEntity},
		{domain.ErrInvalidTransition, http.StatusUnprocessableEntity},
		{domain.ErrInvalidAmount, http.StatusBadRequest},
		{domain.ErrInvalidCurrency, http.StatusBadRequest},
		{fmt.Errorf("wrapped: %w", domain.ErrPayoutNotFound), http.StatusNotFound},
		{errBoom, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			respondDomainError(c, tc.err)

			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d", rec.Code, tc.code)
			}
			want := tc.err.Error()
			if tc.code == http.StatusInternalServerError {
				want = "internal server error"
			}
			if got := errorBody(t, rec); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
		})
	}
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func TestRegisterRoutes(t *testing.T) {
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), newHandler(newRepo(), defaultOpts()))

	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)

	want := []string{
		"GET /api/v1/payouts",
		"GET /api/v1/payouts/:payoutID",
		"POST /api/v1/payouts",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("routes = %v, want %v", got, want)
	}
}
