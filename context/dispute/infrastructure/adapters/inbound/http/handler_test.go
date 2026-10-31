package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

// paymentTenant es el dueño de los pagos que devuelve fakePayments.
var paymentTenant = uuid.NewString()

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeRepo struct {
	byID      map[domain.DisputeID]*domain.Dispute
	byPayment map[string]*domain.Dispute
	listed    []*domain.Dispute
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		byID:      map[domain.DisputeID]*domain.Dispute{},
		byPayment: map[string]*domain.Dispute{},
	}
}

func (r *fakeRepo) Save(ctx context.Context, d *domain.Dispute) error {
	r.byID[d.ID()] = d
	r.byPayment[d.PaymentID()] = d
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, d *domain.Dispute) error { return nil }
func (r *fakeRepo) FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDisputeNotFound
	}
	return d, nil
}
func (r *fakeRepo) FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error) {
	d, ok := r.byPayment[paymentID]
	if !ok {
		return nil, domain.ErrDisputeNotFound
	}
	return d, nil
}
func (r *fakeRepo) ListByTenant(ctx context.Context, t domain.TenantID, s string, l int) ([]*domain.Dispute, error) {
	return r.listed, nil
}
func (r *fakeRepo) ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error) {
	return nil, nil
}
func (r *fakeRepo) ListAll(ctx context.Context, statusFilter string, limit int) ([]*domain.Dispute, error) {
	return r.listed, nil
}

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

// fakePayments implementa application.PaymentReader: devuelve un pago capturado
// válido para cualquier payment_id no vacío.
type fakePayments struct{}

func (fakePayments) FindPayment(ctx context.Context, paymentID string) (domain.PaymentRef, error) {
	if paymentID == "" {
		return domain.PaymentRef{}, domain.ErrDisputeNotFound
	}
	return domain.PaymentRef{ID: paymentID, TenantID: paymentTenant, Status: "captured", Amount: 5000, Currency: "ARS"}, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return testNow }

// testNow es el instante fijo del reloj inyectado en los casos de uso. El deadline
// del body de OpenDispute se corre desde acá (y no desde una fecha absoluta) para
// que el handler reciba siempre un plazo vigente: un deadline hardcodeado vence
// algún día y el test deja de ser determinístico.
var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// openBody arma el JSON de apertura con un deadline futuro relativo a testNow.
func openBody(paymentID string) string {
	return `{"payment_id":"` + paymentID + `","amount":5000,"currency":"ARS",` +
		`"reason":"fraudulent","deadline":"` + testNow.Add(48*time.Hour).Format(time.RFC3339) + `"}`
}

func newHandler(r *fakeRepo) *DisputeHandler {
	return NewDisputeHandler(
		application.NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, fakePublisher{}, fixedClock{}),
		application.NewContestDisputeUseCase(r, fakeTx{}, fixedClock{}),
		application.NewAcceptDisputeUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
		application.NewResolveDisputeUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
		application.NewGetDisputeUseCase(r, fixedClock{}),
		application.NewListDisputesUseCase(r, fixedClock{}),
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
	c.Writer.WriteHeaderNow() // vuelca c.Status() sin body al recorder
	return rec
}

// seedOpen crea una disputa abierta con deadline futuro.
func seedOpen(r *fakeRepo, tid domain.TenantID) *domain.Dispute {
	now := time.Now()
	d, _ := domain.NewDispute(domain.NewDisputeID(),
		domain.PaymentRef{ID: "pay-1", TenantID: tid.String(), Status: "captured", Amount: 5000, Currency: "ARS"},
		"psp-1", 5000, "ARS",
		domain.ReasonFraudulent, now.Add(48*time.Hour), now)
	d.PullEvents()
	r.byID[d.ID()] = d
	r.byPayment[d.PaymentID()] = d
	return d
}

func param(id string) gin.Params { return gin.Params{{Key: "disputeID", Value: id}} }

// ── Open ──────────────────────────────────────────────────────────────────────

func TestOpen(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("creates dispute returns 201", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Open, http.MethodPost, "/disputes",
			openBody("pay-1"),
			tid.String(), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", rec.Code)
		}
		var body struct {
			DisputeID string `json:"dispute_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.DisputeID == "" {
			t.Error("expected dispute_id in response")
		}
	})

	t.Run("missing required field yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Open, http.MethodPost, "/disputes",
			`{"payment_id":"pay-1"}`, tid.String(), nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("duplicate dispute yields 409", func(t *testing.T) {
		r := newRepo()
		seedOpen(r, tid)
		rec := doRequest(newHandler(r).Open, http.MethodPost, "/disputes",
			`{"payment_id":"pay-1","amount":5000,"currency":"ARS","reason":"fraudulent"}`,
			tid.String(), nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("invalid reason yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Open, http.MethodPost, "/disputes",
			`{"payment_id":"pay-1","amount":5000,"currency":"ARS","reason":"bogus"}`,
			tid.String(), nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// ── List / Get ────────────────────────────────────────────────────────────────

func TestListAndGet(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("list returns disputes and count", func(t *testing.T) {
		r := newRepo()
		r.listed = []*domain.Dispute{seedOpen(r, tid)}
		rec := doRequest(newHandler(r).List, http.MethodGet, "/disputes?status=open&limit=10", "", tid.String(), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body struct {
			Count int `json:"count"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Count != 1 {
			t.Errorf("count = %d, want 1", body.Count)
		}
	})

	t.Run("list with invalid tenant yields 500", func(t *testing.T) {
		// Tenant no-uuid en el contexto → error de parseo → default branch (500).
		rec := doRequest(newHandler(newRepo()).List, http.MethodGet, "/disputes", "", "bad-tenant", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("get returns dispute", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid)
		rec := doRequest(newHandler(r).Get, http.MethodGet, "/disputes/x", "", tid.String(), param(d.ID().String()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("get not found yields 404", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Get, http.MethodGet, "/disputes/x", "", tid.String(), param(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("get invalid id yields 500 via default branch", func(t *testing.T) {
		// Un ID no-uuid produce un error de parseo que cae en el default → 500.
		rec := doRequest(newHandler(newRepo()).Get, http.MethodGet, "/disputes/bad", "", tid.String(), param("not-a-uuid"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── Contest ───────────────────────────────────────────────────────────────────

func TestContest(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())
	validBody := `{"evidence":[{"evidence_type":"receipt","reference":"s3://r","description":"d"}],"note":"prueba"}`

	t.Run("valid contest returns 204", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid)
		rec := doRequest(newHandler(r).Contest, http.MethodPost, "/x", validBody, tid.String(), param(d.ID().String()))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("empty evidence array yields 400", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid)
		rec := doRequest(newHandler(r).Contest, http.MethodPost, "/x", `{"evidence":[]}`, tid.String(), param(d.ID().String()))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("not found yields 404", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Contest, http.MethodPost, "/x", validBody, tid.String(), param(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// ── Accept ────────────────────────────────────────────────────────────────────

func TestAccept(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("valid accept returns 204 (empty body allowed)", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid)
		rec := doRequest(newHandler(r).Accept, http.MethodPost, "/x", "", tid.String(), param(d.ID().String()))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("already resolved yields 422", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid)
		_ = d.Accept("", time.Now())
		d.PullEvents()
		rec := doRequest(newHandler(r).Accept, http.MethodPost, "/x", `{"note":"x"}`, tid.String(), param(d.ID().String()))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})
}

// ── Resolve ───────────────────────────────────────────────────────────────────

func TestResolve(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	underReview := func(r *fakeRepo) *domain.Dispute {
		d := seedOpen(r, tid)
		ev, _ := domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d", time.Now())
		_ = d.Contest([]domain.Evidence{ev}, "", time.Now())
		d.PullEvents()
		return d
	}

	t.Run("valid resolve returns 204", func(t *testing.T) {
		r := newRepo()
		d := underReview(r)
		rec := doRequest(newHandler(r).Resolve, http.MethodPost, "/x", `{"outcome":"won"}`, "", param(d.ID().String()))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("missing outcome yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newRepo()).Resolve, http.MethodPost, "/x", `{}`, "", param(uuid.NewString()))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid outcome yields 400", func(t *testing.T) {
		r := newRepo()
		d := underReview(r)
		rec := doRequest(newHandler(r).Resolve, http.MethodPost, "/x", `{"outcome":"bogus"}`, "", param(d.ID().String()))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("resolve from open yields 422", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tid) // sigue open
		rec := doRequest(newHandler(r).Resolve, http.MethodPost, "/x", `{"outcome":"won"}`, "", param(d.ID().String()))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func TestRegisterRoutes(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())
	r := newRepo()
	r.listed = []*domain.Dispute{seedOpen(r, tid)}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(postgres.WithTenantID(c.Request.Context(), tid.String()))
		c.Next()
	})
	allow := func(c *gin.Context) { c.Next() }
	RegisterRoutes(engine.Group("/api/v1"), newHandler(r), RouteGuards{Operator: allow, Manage: allow, Read: allow})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/disputes", nil)
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/disputes status = %d, want 200", rec.Code)
	}
}

// Cada ruta pasa por su guard antes del handler: abrir y resolver son del
// operador de plataforma; contestar y aceptar, de quien gestiona disputas.
func TestRegisterRoutes_Guards(t *testing.T) {
	var hit []string
	deny := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			hit = append(hit, name)
			c.AbortWithStatus(http.StatusForbidden)
		}
	}
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), newHandler(newRepo()), RouteGuards{
		Operator: deny("operator"), Manage: deny("manage"), Read: deny("read"),
	})

	id := uuid.NewString()
	cases := []struct{ method, path, guard string }{
		{http.MethodPost, "/api/v1/disputes", "operator"},
		{http.MethodPost, "/api/v1/disputes/" + id + "/resolve", "operator"},
		{http.MethodPost, "/api/v1/disputes/" + id + "/contest", "manage"},
		{http.MethodPost, "/api/v1/disputes/" + id + "/accept", "manage"},
		{http.MethodGet, "/api/v1/disputes", "read"},
		{http.MethodGet, "/api/v1/disputes/" + id, "read"},
	}
	for _, tc := range cases {
		hit = nil
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusForbidden || len(hit) != 1 || hit[0] != tc.guard {
			t.Errorf("%s %s: status = %d guards = %v, want 403 from %s", tc.method, tc.path, rec.Code, hit, tc.guard)
		}
	}
	if got := len(engine.Routes()); got != len(cases) {
		t.Errorf("routes = %d, want %d", got, len(cases))
	}
}

func TestRegisterRoutes_PanicsWithoutGuards(t *testing.T) {
	allow := func(c *gin.Context) { c.Next() }
	for name, g := range map[string]RouteGuards{
		"operator": {Manage: allow, Read: allow},
		"manage":   {Operator: allow, Read: allow},
		"read":     {Operator: allow, Manage: allow},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("missing %s guard: expected panic", name)
				}
			}()
			RegisterRoutes(gin.New().Group("/"), newHandler(newRepo()), g)
		}()
	}
}

// Las validaciones de apertura y de evidencia caían en el default (500).
func TestRespondError(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{domain.ErrDisputeNotFound, http.StatusNotFound},
		{fmt.Errorf("%w: \"pay-x\"", domain.ErrPaymentNotFound), http.StatusNotFound},
		{domain.ErrDuplicateDispute, http.StatusConflict},
		{fmt.Errorf("dispute repo: update x: %w", domain.ErrOptimisticLock), http.StatusConflict},
		{domain.ErrInvalidTransition, http.StatusUnprocessableEntity},
		{domain.ErrPaymentNotCaptured, http.StatusBadRequest},
		{fmt.Errorf("%w: 9000 > 5000", domain.ErrAmountExceedsPayment), http.StatusBadRequest},
		{fmt.Errorf("%w: USD != ARS", domain.ErrCurrencyMismatch), http.StatusBadRequest},
		{fmt.Errorf("%w: already passed", domain.ErrInvalidDeadline), http.StatusBadRequest},
		{fmt.Errorf("%w: \"selfie\"", domain.ErrInvalidEvidenceType), http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		respondError(c, tc.err)
		if rec.Code != tc.code {
			t.Errorf("%v: status = %d, want %d", tc.err, rec.Code, tc.code)
		}
	}
}
