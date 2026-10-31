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

	"github.com/juantevez/cobros-platform/context/webhook/application"
	"github.com/juantevez/cobros-platform/context/webhook/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

var testTenant = uuid.NewString()

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeEndpoints struct {
	byID      map[domain.EndpointID]*domain.WebhookEndpoint
	saved     []*domain.WebhookEndpoint
	tenantErr error
}

func (r *fakeEndpoints) Save(ctx context.Context, e *domain.WebhookEndpoint) error {
	r.saved = append(r.saved, e)
	r.byID[e.ID()] = e
	return nil
}
func (r *fakeEndpoints) Update(context.Context, *domain.WebhookEndpoint) error { return nil }
func (r *fakeEndpoints) FindByID(ctx context.Context, id domain.EndpointID) (*domain.WebhookEndpoint, error) {
	e, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrEndpointNotFound
	}
	return e, nil
}
func (r *fakeEndpoints) FindByTenant(ctx context.Context, t domain.TenantID) ([]*domain.WebhookEndpoint, error) {
	if r.tenantErr != nil {
		return nil, r.tenantErr
	}
	var out []*domain.WebhookEndpoint
	for _, e := range r.byID {
		if e.TenantID() == t {
			out = append(out, e)
		}
	}
	return out, nil
}
func (r *fakeEndpoints) FindActiveByTenantAndEvent(context.Context, domain.TenantID, string) ([]*domain.WebhookEndpoint, error) {
	return nil, nil
}

type fakeDeliveries struct {
	byID      map[domain.DeliveryID]*domain.WebhookDelivery
	listed    []*domain.WebhookDelivery
	listErr   error
	lastLimit int
}

func (r *fakeDeliveries) Save(context.Context, *domain.WebhookDelivery) error   { return nil }
func (r *fakeDeliveries) Update(context.Context, *domain.WebhookDelivery, int) error { return nil }
func (r *fakeDeliveries) Claim(context.Context, domain.DeliveryID, int, time.Time, time.Time) (bool, error) {
	return true, nil
}
func (r *fakeDeliveries) FindByID(ctx context.Context, id domain.DeliveryID) (*domain.WebhookDelivery, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDeliveryNotFound
	}
	return d, nil
}
func (r *fakeDeliveries) FindByEventAndEndpoint(context.Context, domain.EndpointID, string) (*domain.WebhookDelivery, error) {
	return nil, domain.ErrDeliveryNotFound
}
func (r *fakeDeliveries) ListByTenant(ctx context.Context, t domain.TenantID, limit int) ([]*domain.WebhookDelivery, error) {
	r.lastLimit = limit
	return r.listed, r.listErr
}
func (r *fakeDeliveries) ListDueForRetry(context.Context, time.Time, int) ([]*domain.WebhookDelivery, error) {
	return nil, nil
}

type fakeSecretGen struct{ err error }

func (g *fakeSecretGen) Generate() (string, error) { return "whsec_0123456789abcdef", g.err }

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(context.Context, ...domain.Event) error { return nil }

type fakeDispatcher struct{ calls int }

func (f *fakeDispatcher) Dispatch(ctx context.Context, e *domain.WebhookEndpoint, d *domain.WebhookDelivery) (domain.DeliveryAttempt, error) {
	f.calls++
	return domain.NewDeliveryAttempt(d.AttemptCount()+1, 200, "ok", "", 5), nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }

// ── Helpers ───────────────────────────────────────────────────────────────────

type env struct {
	endpoints  *fakeEndpoints
	deliveries *fakeDeliveries
	dispatcher *fakeDispatcher
	secretGen  *fakeSecretGen
	h          *WebhookHandler
}

func newEnv() *env {
	endpoints := &fakeEndpoints{byID: map[domain.EndpointID]*domain.WebhookEndpoint{}}
	deliveries := &fakeDeliveries{byID: map[domain.DeliveryID]*domain.WebhookDelivery{}}
	dispatcher := &fakeDispatcher{}
	secretGen := &fakeSecretGen{}
	h := NewWebhookHandler(
		application.NewRegisterEndpointUseCase(endpoints, &allowAll{}, secretGen, fakeTx{}, fakePublisher{}),
		application.NewDeactivateEndpointUseCase(endpoints, fakeTx{}, fakePublisher{}),
		application.NewListEndpointsUseCase(endpoints),
		application.NewListDeliveriesUseCase(deliveries),
		application.NewGetDeliveryUseCase(deliveries),
		application.NewRetryDeliveryUseCase(endpoints, deliveries, dispatcher, fakeTx{}, fakePublisher{}, fixedClock{}),
	)
	return &env{endpoints: endpoints, deliveries: deliveries, dispatcher: dispatcher, secretGen: secretGen, h: h}
}

func (e *env) seedEndpoint(tenantID string) *domain.WebhookEndpoint {
	ep, _ := domain.NewWebhookEndpoint(domain.NewEndpointID(), domain.TenantID(tenantID),
		"https://merchant.example.com/hooks", "whsec_x", []string{"payment.captured"}, "")
	e.endpoints.byID[ep.ID()] = ep
	return ep
}

func (e *env) seedDelivery(ep *domain.WebhookEndpoint, status domain.DeliveryStatus) *domain.WebhookDelivery {
	now := time.Now().UTC()
	d := domain.ReconstituteDelivery(domain.NewDeliveryID(), ep.ID(), ep.TenantID(), "payment.captured", "evt-1",
		json.RawMessage(`{}`), status, 1, &now, nil, now, now)
	e.deliveries.byID[d.ID()] = d
	return d
}

func doRequest(h gin.HandlerFunc, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(postgres.WithTenantID(req.Context(), testTenant))
	c.Request = req
	c.Params = params
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

func endpointParam(id string) gin.Params { return gin.Params{{Key: "endpointID", Value: id}} }
func deliveryParam(id string) gin.Params { return gin.Params{{Key: "deliveryID", Value: id}} }

// ── Endpoints ─────────────────────────────────────────────────────────────────

func TestRegisterEndpoint(t *testing.T) {
	t.Run("returns 201 with secret shown once", func(t *testing.T) {
		e := newEnv()
		rec := doRequest(e.h.RegisterEndpoint, http.MethodPost, "/x",
			`{"url":"https://merchant.example.com/hooks","events":["payment.captured"],"description":"prod"}`, nil)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if body["secret"] != "whsec_0123456789abcdef" || body["secret_hint"] != "...cdef" || body["endpoint_id"] == "" || body["note"] == "" {
			t.Errorf("body = %v", body)
		}
		if len(e.endpoints.saved) != 1 || e.endpoints.saved[0].TenantID() != domain.TenantID(testTenant) {
			t.Error("endpoint not saved for authenticated tenant")
		}
	})

	for _, body := range []string{`{bad`, `{"events":["a.b"]}`, `{"url":"https://x.example.com"}`, `{"url":"https://x.example.com","events":[]}`} {
		t.Run("400 on "+body, func(t *testing.T) {
			e := newEnv()
			rec := doRequest(e.h.RegisterEndpoint, http.MethodPost, "/x", body, nil)
			if rec.Code != http.StatusBadRequest || errorBody(t, rec) != "invalid request body" {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestRegisterEndpoint_DomainValidation(t *testing.T) {
	cases := []struct{ name, body, wantErr string }{
		{"invalid url", `{"url":"ftp://x.example.com","events":["a.b"]}`, "invalid webhook URL"},
		{"url without host", `{"url":"/hooks","events":["a.b"]}`, "invalid webhook URL"},
		{"only blank events", `{"url":"https://x.example.com","events":[""]}`, domain.ErrNoEventsSubscribed.Error()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv()
			rec := doRequest(e.h.RegisterEndpoint, http.MethodPost, "/x", tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
			if got := errorBody(t, rec); !strings.HasPrefix(got, tc.wantErr) {
				t.Errorf("error = %q, want prefix %q", got, tc.wantErr)
			}
			if len(e.endpoints.saved) != 0 {
				t.Error("nothing should be saved")
			}
		})
	}
}

func TestRegisterEndpoint_InternalError(t *testing.T) {
	e := newEnv()
	e.secretGen.err = errBoom
	rec := doRequest(e.h.RegisterEndpoint, http.MethodPost, "/x", `{"url":"https://x.example.com","events":["a.b"]}`, nil)
	if rec.Code != http.StatusInternalServerError || errorBody(t, rec) != "internal server error" {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "whsec_") {
		t.Error("secret must not leak on error")
	}
}

func TestListEndpoints_RepoError(t *testing.T) {
	e := newEnv()
	e.endpoints.tenantErr = errBoom
	if rec := doRequest(e.h.ListEndpoints, http.MethodGet, "/x", "", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestListEndpoints(t *testing.T) {
	e := newEnv()
	e.seedEndpoint(testTenant)
	e.seedEndpoint(uuid.NewString()) // de otro tenant

	rec := doRequest(e.h.ListEndpoints, http.MethodGet, "/x", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Endpoints []application.EndpointView `json:"endpoints"`
		Count     int                        `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Count != 1 || len(body.Endpoints) != 1 {
		t.Errorf("count = %d", body.Count)
	}
	if strings.Contains(rec.Body.String(), `"secret"`) {
		t.Error("secret must not be exposed when listing")
	}
}

func TestDeactivateEndpoint(t *testing.T) {
	t.Run("returns 204", func(t *testing.T) {
		e := newEnv()
		ep := e.seedEndpoint(testTenant)
		rec := doRequest(e.h.DeactivateEndpoint, http.MethodDelete, "/x", "", endpointParam(ep.ID().String()))
		if rec.Code != http.StatusNoContent || ep.Active() {
			t.Fatalf("status = %d active = %v", rec.Code, ep.Active())
		}
	})

	t.Run("endpoint of another tenant returns 404", func(t *testing.T) {
		e := newEnv()
		ep := e.seedEndpoint(uuid.NewString())
		rec := doRequest(e.h.DeactivateEndpoint, http.MethodDelete, "/x", "", endpointParam(ep.ID().String()))
		if rec.Code != http.StatusNotFound || !ep.Active() {
			t.Fatalf("status = %d active = %v", rec.Code, ep.Active())
		}
	})
}

// ── Deliveries ────────────────────────────────────────────────────────────────

func TestListDeliveries(t *testing.T) {
	t.Run("returns deliveries with count and limit", func(t *testing.T) {
		e := newEnv()
		ep := e.seedEndpoint(testTenant)
		e.deliveries.listed = []*domain.WebhookDelivery{e.seedDelivery(ep, domain.StatusFailed)}

		rec := doRequest(e.h.ListDeliveries, http.MethodGet, "/x?limit=7", "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":1`) {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
		}
		if e.deliveries.lastLimit != 7 {
			t.Errorf("limit = %d, want 7", e.deliveries.lastLimit)
		}
	})

	for _, q := range []string{"/x", "/x?limit=abc"} {
		t.Run("default limit for "+q, func(t *testing.T) {
			e := newEnv()
			doRequest(e.h.ListDeliveries, http.MethodGet, q, "", nil)
			if e.deliveries.lastLimit != 50 {
				t.Errorf("limit = %d, want 50", e.deliveries.lastLimit)
			}
		})
	}

	t.Run("repo error returns 500", func(t *testing.T) {
		e := newEnv()
		e.deliveries.listErr = errBoom
		if rec := doRequest(e.h.ListDeliveries, http.MethodGet, "/x", "", nil); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestGetDelivery(t *testing.T) {
	t.Run("returns delivery", func(t *testing.T) {
		e := newEnv()
		d := e.seedDelivery(e.seedEndpoint(testTenant), domain.StatusFailed)
		rec := doRequest(e.h.GetDelivery, http.MethodGet, "/x", "", deliveryParam(d.ID().String()))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), d.ID().String()) {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
		}
	})

	t.Run("delivery of another tenant returns 404", func(t *testing.T) {
		e := newEnv()
		d := e.seedDelivery(e.seedEndpoint(uuid.NewString()), domain.StatusFailed)
		if rec := doRequest(e.h.GetDelivery, http.MethodGet, "/x", "", deliveryParam(d.ID().String())); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestRetryDelivery(t *testing.T) {
	t.Run("dispatches and returns 204", func(t *testing.T) {
		e := newEnv()
		d := e.seedDelivery(e.seedEndpoint(testTenant), domain.StatusFailed)
		rec := doRequest(e.h.RetryDelivery, http.MethodPost, "/x", "", deliveryParam(d.ID().String()))
		if rec.Code != http.StatusNoContent || e.dispatcher.calls != 1 || d.Status() != domain.StatusDelivered {
			t.Fatalf("status = %d calls = %d delivery = %q", rec.Code, e.dispatcher.calls, d.Status())
		}
	})

	t.Run("already delivered returns 422", func(t *testing.T) {
		e := newEnv()
		d := e.seedDelivery(e.seedEndpoint(testTenant), domain.StatusDelivered)
		rec := doRequest(e.h.RetryDelivery, http.MethodPost, "/x", "", deliveryParam(d.ID().String()))
		if rec.Code != http.StatusUnprocessableEntity || e.dispatcher.calls != 0 {
			t.Fatalf("status = %d calls = %d", rec.Code, e.dispatcher.calls)
		}
	})

	t.Run("unknown delivery returns 404", func(t *testing.T) {
		e := newEnv()
		if rec := doRequest(e.h.RetryDelivery, http.MethodPost, "/x", "", deliveryParam(uuid.NewString())); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// ── respondError / RegisterRoutes ─────────────────────────────────────────────

// allowAll acepta cualquier destino: los handlers no prueban el filtro SSRF.
type allowAll struct{}

func (allowAll) Check(context.Context, string) error { return nil }

func TestRespondError(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{domain.ErrEndpointNotFound, http.StatusNotFound},
		{domain.ErrDeliveryNotFound, http.StatusNotFound},
		{domain.ErrDeliveryNotRetryable, http.StatusUnprocessableEntity},
		{fmt.Errorf("delivery repo: update x: %w", domain.ErrDeliveryInProgress), http.StatusConflict},
		{domain.ErrEndpointInactive, http.StatusUnprocessableEntity},
		{domain.ErrEndpointURLEmpty, http.StatusBadRequest},
		{fmt.Errorf("%w: missing host", domain.ErrInvalidEndpointURL), http.StatusBadRequest},
		{domain.ErrNoEventsSubscribed, http.StatusBadRequest},
		{fmt.Errorf("%w: 127.0.0.1", domain.ErrDestinationNotAllowed), http.StatusBadRequest},
		{fmt.Errorf("wrapped: %w", domain.ErrDeliveryNotFound), http.StatusNotFound},
		{errBoom, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			respondError(c, tc.err)

			want := tc.err.Error()
			if tc.code == http.StatusInternalServerError {
				want = "internal server error"
			}
			if rec.Code != tc.code || errorBody(t, rec) != want {
				t.Errorf("got %d %q, want %d %q", rec.Code, errorBody(t, rec), tc.code, want)
			}
		})
	}
}

func TestRegisterRoutes(t *testing.T) {
	denied := 0
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), newEnv().h, RouteGuards{
		Manage: func(c *gin.Context) { denied++; c.AbortWithStatus(http.StatusForbidden) },
	})

	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)
	want := []string{
		"DELETE /api/v1/webhooks/endpoints/:endpointID",
		"GET /api/v1/webhooks/deliveries",
		"GET /api/v1/webhooks/deliveries/:deliveryID",
		"GET /api/v1/webhooks/endpoints",
		"POST /api/v1/webhooks/deliveries/:deliveryID/retry",
		"POST /api/v1/webhooks/endpoints",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("routes = %v\nwant %v", got, want)
	}

	// Cada ruta pasa por el guard antes del handler.
	for _, route := range want {
		method, path, _ := strings.Cut(route, " ")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 from the guard", route, rec.Code)
		}
	}
	if denied != len(want) {
		t.Errorf("guard ran %d times, want %d", denied, len(want))
	}
}

func TestRegisterRoutes_PanicsWithoutGuard(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	RegisterRoutes(gin.New().Group("/"), newEnv().h, RouteGuards{})
}
