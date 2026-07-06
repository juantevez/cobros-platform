package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeAlertRepo struct {
	byID      map[domain.AlertID]*domain.Alert
	listed    []*domain.Alert
	listErr   error
	updateErr error
}

func newAlertRepo() *fakeAlertRepo {
	return &fakeAlertRepo{byID: map[domain.AlertID]*domain.Alert{}}
}

func (r *fakeAlertRepo) Save(ctx context.Context, a *domain.Alert) error { return nil }
func (r *fakeAlertRepo) Update(ctx context.Context, a *domain.Alert) error {
	return r.updateErr
}
func (r *fakeAlertRepo) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAlertNotFound
	}
	return a, nil
}
func (r *fakeAlertRepo) ListByTenant(ctx context.Context, t domain.TenantID, s string, l int) ([]*domain.Alert, error) {
	return r.listed, r.listErr
}

type fakeWatchlist struct {
	entries []domain.WatchlistEntry
	listErr error
	addErr  error
	added   []domain.WatchlistEntry
}

func (w *fakeWatchlist) Screen(ctx context.Context, n string) ([]domain.Match, error) {
	return nil, nil
}
func (w *fakeWatchlist) Add(ctx context.Context, e domain.WatchlistEntry, n string, at time.Time) error {
	if w.addErr != nil {
		return w.addErr
	}
	w.added = append(w.added, e)
	return nil
}
func (w *fakeWatchlist) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	return w.entries, w.listErr
}

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }

func newHandler(r *fakeAlertRepo, w *fakeWatchlist) *ComplianceHandler {
	return NewComplianceHandler(
		application.NewListAlertsUseCase(r),
		application.NewGetAlertUseCase(r),
		application.NewResolveAlertUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
		application.NewAddWatchlistEntryUseCase(w, fixedClock{}),
		application.NewListWatchlistUseCase(w),
	)
}

// doRequest arma un contexto Gin con tenant en el contexto de la request.
func doRequest(h gin.HandlerFunc, method, target, body, tenantID string, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	if tenantID != "" {
		req = req.WithContext(postgres.WithTenantID(req.Context(), tenantID))
	}
	c.Request = req
	c.Params = params
	h(c)
	// Los handlers con c.Status() sin body no vuelcan el código al recorder
	// hasta el final del ciclo del engine; lo forzamos para poder afirmarlo.
	c.Writer.WriteHeaderNow()
	return rec
}

func seedAlert(r *fakeAlertRepo, tid domain.TenantID) *domain.Alert {
	a := domain.NewAlert(domain.NewAlertID(), tid, domain.AlertSanctionsMatch,
		domain.RiskHigh, "subj", 95, nil, time.Now())
	a.PullEvents()
	r.byID[a.ID()] = a
	return a
}

// ── ListAlerts ────────────────────────────────────────────────────────────────

func TestListAlerts(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("returns alerts and count", func(t *testing.T) {
		r := newAlertRepo()
		r.listed = []*domain.Alert{seedAlert(r, tid)}
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts?status=open&limit=10", "", tid.String(), nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body struct {
			Alerts []application.AlertView `json:"alerts"`
			Count  int                     `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad json: %v", err)
		}
		if body.Count != 1 || len(body.Alerts) != 1 {
			t.Errorf("count/alerts mismatch: %+v", body)
		}
	})

	t.Run("use case error yields 500", func(t *testing.T) {
		r := newAlertRepo()
		r.listErr = errors.New("boom")
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts", "", tid.String(), nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── GetAlert ──────────────────────────────────────────────────────────────────

func TestGetAlert(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("returns alert", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		rec := doRequest(newHandler(r, &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/compliance/alerts/"+a.ID().String(), "", tid.String(),
			gin.Params{{Key: "alertID", Value: a.ID().String()}})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("not found yields 404", func(t *testing.T) {
		r := newAlertRepo()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/compliance/alerts/x", "", tid.String(),
			gin.Params{{Key: "alertID", Value: uuid.NewString()}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid alert id yields 400", func(t *testing.T) {
		r := newAlertRepo()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/compliance/alerts/bad", "", tid.String(),
			gin.Params{{Key: "alertID", Value: "not-a-uuid"}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// ── ResolveAlert ──────────────────────────────────────────────────────────────

func TestResolveAlert(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("resolves and returns 204", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ResolveAlert, http.MethodPost,
			"/x", `{"disposition":"cleared","note":"fp"}`, tid.String(),
			gin.Params{{Key: "alertID", Value: a.ID().String()}})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("invalid body yields 400", func(t *testing.T) {
		r := newAlertRepo()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ResolveAlert, http.MethodPost,
			"/x", `{bad`, tid.String(),
			gin.Params{{Key: "alertID", Value: uuid.NewString()}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("not found yields 404", func(t *testing.T) {
		r := newAlertRepo()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ResolveAlert, http.MethodPost,
			"/x", `{"disposition":"cleared"}`, tid.String(),
			gin.Params{{Key: "alertID", Value: uuid.NewString()}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("already resolved yields 409", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		_ = a.Resolve(domain.StatusConfirmed, "", time.Now())
		a.PullEvents()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ResolveAlert, http.MethodPost,
			"/x", `{"disposition":"cleared"}`, tid.String(),
			gin.Params{{Key: "alertID", Value: a.ID().String()}})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("invalid disposition yields 400", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ResolveAlert, http.MethodPost,
			"/x", `{"disposition":"maybe"}`, tid.String(),
			gin.Params{{Key: "alertID", Value: a.ID().String()}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// ── Watchlist ─────────────────────────────────────────────────────────────────

func TestListWatchlist(t *testing.T) {
	t.Run("returns entries", func(t *testing.T) {
		w := &fakeWatchlist{entries: []domain.WatchlistEntry{{ID: "1", FullName: "A", ListType: "pep"}}}
		rec := doRequest(newHandler(newAlertRepo(), w).ListWatchlist, http.MethodGet,
			"/compliance/watchlist?limit=100", "", "", nil)
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

	t.Run("error yields 500", func(t *testing.T) {
		w := &fakeWatchlist{listErr: errors.New("boom")}
		rec := doRequest(newHandler(newAlertRepo(), w).ListWatchlist, http.MethodGet,
			"/compliance/watchlist", "", "", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestAddWatchlistEntry(t *testing.T) {
	t.Run("creates entry returns 201", func(t *testing.T) {
		w := &fakeWatchlist{}
		rec := doRequest(newHandler(newAlertRepo(), w).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"full_name":"Juan Perez","list_type":"pep","country":"AR"}`, "", nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", rec.Code)
		}
		if len(w.added) != 1 {
			t.Errorf("expected 1 entry added, got %d", len(w.added))
		}
	})

	t.Run("missing full_name yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"list_type":"pep"}`, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing list_type yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"full_name":"x"}`, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("use case error yields 400", func(t *testing.T) {
		w := &fakeWatchlist{addErr: errors.New("boom")}
		rec := doRequest(newHandler(newAlertRepo(), w).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"full_name":"x","list_type":"pep"}`, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func TestRegisterRoutes(t *testing.T) {
	w := &fakeWatchlist{entries: []domain.WatchlistEntry{{ID: "1", FullName: "A", ListType: "pep"}}}
	r := gin.New()
	RegisterRoutes(r.Group("/api/v1"), newHandler(newAlertRepo(), w))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/compliance/watchlist", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/compliance/watchlist status = %d, want 200", rec.Code)
	}
}
