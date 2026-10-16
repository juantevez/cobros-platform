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
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeRepo struct {
	byID  map[domain.PaymentID]*domain.Payment
	byKey map[string]*domain.Payment
}

func newRepo() *fakeRepo {
	return &fakeRepo{byID: map[domain.PaymentID]*domain.Payment{}, byKey: map[string]*domain.Payment{}}
}

func (r *fakeRepo) Save(ctx context.Context, p *domain.Payment) error {
	r.byID[p.ID()] = p
	r.byKey[p.IdempotencyKey()] = p
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, p *domain.Payment) error { return nil }
func (r *fakeRepo) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPaymentNotFound
	}
	return p, nil
}
func (r *fakeRepo) FindByIdempotencyKey(ctx context.Context, t domain.TenantID, key string) (*domain.Payment, error) {
	p, ok := r.byKey[key]
	if !ok {
		return nil, domain.ErrPaymentNotFound
	}
	return p, nil
}

type fakePSP struct{ captureErr, refundErr error }

func (f fakePSP) AuthorizeAndCapture(ctx context.Context, req application.PSPCaptureRequest) (application.PSPCaptureResult, error) {
	if f.captureErr != nil {
		return application.PSPCaptureResult{}, f.captureErr
	}
	return application.PSPCaptureResult{PSPReference: "PSP-1", PSPFee: 150, Status: "approved"}, nil
}
func (f fakePSP) Refund(ctx context.Context, req application.PSPRefundRequest) (application.PSPRefundResult, error) {
	if f.refundErr != nil {
		return application.PSPRefundResult{}, f.refundErr
	}
	return application.PSPRefundResult{PSPReference: "REF-1"}, nil
}
func (fakePSP) Name() string { return "fake" }

type fakeRouter struct{ psp fakePSP }

func (r fakeRouter) Route(ctx context.Context, m domain.PaymentMethod, t domain.TenantID) (application.PSPAdapter, error) {
	return r.psp, nil
}

type fakeRisk struct{ approved bool }

func (r fakeRisk) Evaluate(ctx context.Context, p *domain.Payment) (application.RiskDecision, error) {
	return application.RiskDecision{Approved: r.approved, Reason: "risky"}, nil
}

type fakeFees struct{}

func (fakeFees) Calculate(ctx context.Context, t domain.TenantID, amount domain.Money, m domain.PaymentMethod) (domain.Money, error) {
	return domain.ReconstituteMoney(300, amount.Currency()), nil
}

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

// ── Helpers ───────────────────────────────────────────────────────────────────

type handlerOpts struct {
	riskApproved bool
	psp          fakePSP
}

func defaultOpts() handlerOpts { return handlerOpts{riskApproved: true} }

func newHandler(r *fakeRepo, o handlerOpts) *PaymentHandler {
	router := fakeRouter{o.psp}
	return NewPaymentHandler(
		application.NewProcessPaymentUseCase(r, router, fakeRisk{o.riskApproved}, fakeFees{}, fakeTx{}, fakePublisher{}),
		application.NewRefundPaymentUseCase(r, router, fakeTx{}, fakePublisher{}),
		application.NewGetPaymentUseCase(r),
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

func seedPayment(r *fakeRepo, tenantID string, status domain.PaymentStatus) *domain.Payment {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	p := domain.ReconstitutePayment(
		domain.NewPaymentID(), domain.TenantID(tenantID), "seed-"+uuid.NewString(), "",
		domain.ReconstituteMoney(10_000, "ARS"), domain.ReconstituteMoney(300, "ARS"), domain.ReconstituteMoney(150, "ARS"),
		domain.PayerInfo{}, domain.MethodCard, "fake", "PSP-1", status, "", nil,
		nil, &now, nil, now, now,
	)
	r.byID[p.ID()] = p
	r.byKey[p.IdempotencyKey()] = p
	return p
}

func param(id string) gin.Params { return gin.Params{{Key: "paymentID", Value: id}} }

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

const validBody = `{"idempotency_key":"order-1","amount":10000,"currency":"ARS","payment_method":"card","payment_token":"tok","payer_name":"Ana","metadata":{"k":"v"}}`

// ── Process ───────────────────────────────────────────────────────────────────

func TestProcess(t *testing.T) {
	tid := uuid.NewString()

	t.Run("new payment returns 201", func(t *testing.T) {
		r := newRepo()
		rec := doRequest(newHandler(r, defaultOpts()).Process, http.MethodPost, "/payments", validBody, tid, nil)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var res application.ProcessPaymentResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if res.PaymentID == "" || res.Status != "captured" || res.Amount != 10_000 || res.PlatformFee != 300 || res.WasExisting {
			t.Errorf("unexpected result: %+v", res)
		}
		p := r.byKey["order-1"]
		if p == nil || p.PayerInfo().Name != "Ana" || p.Metadata()["k"] != "v" {
			t.Error("request fields not propagated to payment")
		}
	})

	t.Run("idempotent retry returns 200 with existing payment", func(t *testing.T) {
		r := newRepo()
		h := newHandler(r, defaultOpts())
		first := doRequest(h.Process, http.MethodPost, "/payments", validBody, tid, nil)
		second := doRequest(h.Process, http.MethodPost, "/payments", validBody, tid, nil)

		if first.Code != http.StatusCreated || second.Code != http.StatusOK {
			t.Fatalf("codes = %d, %d; want 201, 200", first.Code, second.Code)
		}
		var a, b application.ProcessPaymentResult
		_ = json.Unmarshal(first.Body.Bytes(), &a)
		_ = json.Unmarshal(second.Body.Bytes(), &b)
		if a.PaymentID != b.PaymentID || !b.WasExisting {
			t.Errorf("retry should return same payment: %+v vs %+v", a, b)
		}
	})

	badBodies := []struct{ name, body string }{
		{"malformed json", `{bad`},
		{"missing idempotency key", `{"amount":100,"currency":"ARS","payment_method":"card","payment_token":"tok"}`},
		{"zero amount", `{"idempotency_key":"k","amount":0,"currency":"ARS","payment_method":"card","payment_token":"tok"}`},
		{"negative amount", `{"idempotency_key":"k","amount":-5,"currency":"ARS","payment_method":"card","payment_token":"tok"}`},
		{"missing currency", `{"idempotency_key":"k","amount":100,"payment_method":"card","payment_token":"tok"}`},
		{"missing method", `{"idempotency_key":"k","amount":100,"currency":"ARS","payment_token":"tok"}`},
		{"missing token", `{"idempotency_key":"k","amount":100,"currency":"ARS","payment_method":"card"}`},
	}
	for _, tc := range badBodies {
		t.Run("400 on "+tc.name, func(t *testing.T) {
			r := newRepo()
			rec := doRequest(newHandler(r, defaultOpts()).Process, http.MethodPost, "/payments", tc.body, tid, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorBody(t, rec); got != "invalid request body" {
				t.Errorf("error = %q", got)
			}
			if len(r.byID) != 0 {
				t.Error("nothing should be saved")
			}
		})
	}

	t.Run("invalid method returns 400", func(t *testing.T) {
		body := strings.Replace(validBody, `"card"`, `"cash"`, 1)
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Process, http.MethodPost, "/payments", body, tid, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid currency returns 400", func(t *testing.T) {
		body := strings.Replace(validBody, `"ARS"`, `"PESOS"`, 1)
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Process, http.MethodPost, "/payments", body, tid, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("risk rejection returns 422", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), handlerOpts{riskApproved: false}).Process, http.MethodPost, "/payments", validBody, tid, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if got := errorBody(t, rec); got != domain.ErrRiskRejected.Error() {
			t.Errorf("error = %q", got)
		}
	})

	t.Run("psp rejection returns 500 without leaking details", func(t *testing.T) {
		o := defaultOpts()
		o.psp.captureErr = errors.New("card 4111 declined")
		rec := doRequest(newHandler(newRepo(), o).Process, http.MethodPost, "/payments", validBody, tid, nil)
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

	t.Run("returns payment view", func(t *testing.T) {
		r := newRepo()
		p := seedPayment(r, tid, domain.StatusCaptured)

		rec := doRequest(newHandler(r, defaultOpts()).Get, http.MethodGet, "/payments/x", "", tid, param(p.ID().String()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var v application.PaymentView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if v.ID != p.ID().String() || v.Status != "captured" || v.NetAmount != 9_550 {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("unknown payment returns 404", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Get, http.MethodGet, "/payments/x", "", tid, param(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("payment of another tenant returns 404", func(t *testing.T) {
		r := newRepo()
		p := seedPayment(r, uuid.NewString(), domain.StatusCaptured)
		rec := doRequest(newHandler(r, defaultOpts()).Get, http.MethodGet, "/payments/x", "", tid, param(p.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// ── Refund ────────────────────────────────────────────────────────────────────

func TestRefund(t *testing.T) {
	tid := uuid.NewString()

	t.Run("refunds captured payment", func(t *testing.T) {
		r := newRepo()
		p := seedPayment(r, tid, domain.StatusCaptured)

		rec := doRequest(newHandler(r, defaultOpts()).Refund, http.MethodPost, "/payments/x/refund", "", tid, param(p.ID().String()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var res application.RefundPaymentResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		want := application.RefundPaymentResult{PaymentID: p.ID().String(), PSPReference: "REF-1", Status: "refunded"}
		if res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
	})

	t.Run("not captured returns 422", func(t *testing.T) {
		r := newRepo()
		p := seedPayment(r, tid, domain.StatusFailed)
		rec := doRequest(newHandler(r, defaultOpts()).Refund, http.MethodPost, "/payments/x/refund", "", tid, param(p.ID().String()))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})

	t.Run("unknown payment returns 404", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo(), defaultOpts()).Refund, http.MethodPost, "/payments/x/refund", "", tid, param(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("psp refund failure returns 500", func(t *testing.T) {
		r := newRepo()
		p := seedPayment(r, tid, domain.StatusCaptured)
		o := defaultOpts()
		o.psp.refundErr = errBoom
		rec := doRequest(newHandler(r, o).Refund, http.MethodPost, "/payments/x/refund", "", tid, param(p.ID().String()))
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
		{domain.ErrPaymentNotFound, http.StatusNotFound},
		{domain.ErrPaymentAlreadyExists, http.StatusConflict},
		{domain.ErrRiskRejected, http.StatusUnprocessableEntity},
		{domain.ErrNotCaptured, http.StatusUnprocessableEntity},
		{domain.ErrAlreadyCaptured, http.StatusUnprocessableEntity},
		{domain.ErrInvalidTransition, http.StatusUnprocessableEntity},
		{domain.ErrInvalidAmount, http.StatusBadRequest},
		{domain.ErrInvalidCurrency, http.StatusBadRequest},
		{domain.ErrInvalidMethod, http.StatusBadRequest},
		{fmt.Errorf("find payment: %w", domain.ErrPaymentNotFound), http.StatusNotFound},
		{domain.ErrRefundExceedsAmount, http.StatusInternalServerError},
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
		"GET /api/v1/payments/:paymentID",
		"POST /api/v1/payments",
		"POST /api/v1/payments/:paymentID/refund",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("routes = %v, want %v", got, want)
	}
}
