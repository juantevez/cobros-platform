package http

import (
	"context"
	"encoding/json"
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

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }

func newHandler(r *fakeRepo) *DisputeHandler {
	return NewDisputeHandler(
		application.NewOpenDisputeUseCase(r, fakeTx{}, fakePublisher{}),
		application.NewContestDisputeUseCase(r, fakeTx{}, fixedClock{}),
		application.NewAcceptDisputeUseCase(r, fakeTx{}, fakePublisher{}),
		application.NewResolveDisputeUseCase(r, fakeTx{}, fakePublisher{}),
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
	d, _ := domain.NewDispute(domain.NewDisputeID(), tid, "pay-1", "psp-1", 5000, "ARS",
		domain.ReasonFraudulent, time.Now().Add(48*time.Hour))
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
			`{"payment_id":"pay-1","amount":5000,"currency":"ARS","reason":"fraudulent","deadline":"2026-08-01T00:00:00Z"}`,
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
		_ = d.Accept("")
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
		_ = d.Contest([]domain.Evidence{domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d")}, "", time.Now())
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
	RegisterRoutes(engine.Group("/api/v1"), newHandler(r))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/disputes", nil)
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/disputes status = %d, want 200", rec.Code)
	}
}
