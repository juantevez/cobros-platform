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
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

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
	updated   []*domain.Alert

	gotFilter application.AlertFilter
}

func newAlertRepo() *fakeAlertRepo {
	return &fakeAlertRepo{byID: map[domain.AlertID]*domain.Alert{}}
}

func (r *fakeAlertRepo) Save(ctx context.Context, a *domain.Alert) error { return nil }
func (r *fakeAlertRepo) SaveUnlessRecent(ctx context.Context, a *domain.Alert, p string, since time.Time) error {
	return nil
}
func (r *fakeAlertRepo) Update(ctx context.Context, a *domain.Alert, from domain.AlertStatus) error {
	r.updated = append(r.updated, a)
	return r.updateErr
}
func (r *fakeAlertRepo) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAlertNotFound
	}
	return a, nil
}
func (r *fakeAlertRepo) List(ctx context.Context, f application.AlertFilter, l int) ([]*domain.Alert, error) {
	r.gotFilter = f
	return r.listed, r.listErr
}

type fakeWatchlist struct {
	entries   []domain.WatchlistEntry
	listErr   error
	addErr    error
	removeErr error
	added     []domain.WatchlistEntry
	removed   []string
	active    []domain.WatchlistEntry
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
func (w *fakeWatchlist) Remove(ctx context.Context, id string, at time.Time) error {
	if w.removeErr != nil {
		return w.removeErr
	}
	w.removed = append(w.removed, id)
	return nil
}
func (w *fakeWatchlist) ListActiveBySource(ctx context.Context, source string) ([]domain.WatchlistEntry, error) {
	return w.active, nil
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
		AlertUseCases{
			List:           application.NewListAlertsUseCase(r),
			Get:            application.NewGetAlertUseCase(r),
			Resolve:        application.NewResolveAlertUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
			Approve:        application.NewApproveAlertProposalUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
			RejectProposal: application.NewRejectAlertProposalUseCase(r, fakeTx{}, fakePublisher{}, fixedClock{}),
		},
		WatchlistUseCases{
			Add:    application.NewAddWatchlistEntryUseCase(w, fixedClock{}),
			Remove: application.NewRemoveWatchlistEntryUseCase(w, fixedClock{}),
			List:   application.NewListWatchlistUseCase(w),
			Import: application.NewImportWatchlistUseCase(w, fakeTx{}, fixedClock{}),
		},
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
		ctx := postgres.WithTenantID(req.Context(), tenantID)
		req = req.WithContext(postgres.WithActor(ctx, "user-42"))
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
			"/compliance/alerts?status=open&limit=10", "", "", nil)

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
		if r.gotFilter.TenantID != "" {
			t.Errorf("no tenant filter expected (cross-tenant), got %q", r.gotFilter.TenantID)
		}
	})

	t.Run("tenant_id query param filters by merchant", func(t *testing.T) {
		r := newAlertRepo()
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts?tenant_id="+tid.String()+"&status=pending_second_approval", "", uuid.NewString(), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if r.gotFilter.TenantID != tid || r.gotFilter.Status != domain.StatusPendingSecondApproval {
			t.Errorf("filter = %+v", r.gotFilter)
		}
	})

	t.Run("invalid status filter yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts?status=bogus", "", "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid tenant_id yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts?tenant_id=nope", "", "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("use case error yields 500", func(t *testing.T) {
		r := newAlertRepo()
		r.listErr = errors.New("boom")
		rec := doRequest(newHandler(r, &fakeWatchlist{}).ListAlerts, http.MethodGet,
			"/compliance/alerts", "", "", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── GetAlert ──────────────────────────────────────────────────────────────────

func TestGetAlert(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("returns alert of any merchant", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		rec := doRequest(newHandler(r, &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/x", "", uuid.NewString(), gin.Params{{Key: "alertID", Value: a.ID().String()}})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("not found yields 404", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/x", "", "", gin.Params{{Key: "alertID", Value: uuid.NewString()}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid alert id yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).GetAlert, http.MethodGet,
			"/x", "", "", gin.Params{{Key: "alertID", Value: "not-a-uuid"}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// ── Revisión (resolve / approve / reject) ─────────────────────────────────────

// doAs ejecuta el handler autenticado como `actor` (userID del analista).
func doAs(h gin.HandlerFunc, actor, body string, alertID domain.AlertID) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req = req.WithContext(postgres.WithActor(req.Context(), actor))
	c.Request = req
	c.Params = gin.Params{{Key: "alertID", Value: alertID.String()}}
	h(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func decodeView(t *testing.T, rec *httptest.ResponseRecorder) application.AlertView {
	t.Helper()
	var v application.AlertView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad json %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestReviewFlow(t *testing.T) {
	tid, _ := domain.ParseTenantID(uuid.NewString())

	t.Run("single-step alert is closed by one analyst", func(t *testing.T) {
		r := newAlertRepo()
		a := domain.NewAlert(domain.NewAlertID(), tid, domain.AlertTransactionThreshold,
			domain.RiskMedium, "pay-1", 70, nil, time.Now())
		a.PullEvents()
		r.byID[a.ID()] = a
		h := newHandler(r, &fakeWatchlist{})

		rec := doAs(h.ResolveAlert, "officer-1", `{"disposition":"cleared","note":"fp"}`, a.ID())
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if v := decodeView(t, rec); v.Status != "cleared" || v.ResolvedBy != "officer-1" || v.RequiresSecondApproval {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("four-eyes: propose, same analyst blocked, second analyst approves", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid) // sanctions_match / high
		h := newHandler(r, &fakeWatchlist{})

		rec := doAs(h.ResolveAlert, "officer-1", `{"disposition":"confirmed","note":"coincide"}`, a.ID())
		if rec.Code != http.StatusOK {
			t.Fatalf("propose status = %d: %s", rec.Code, rec.Body.String())
		}
		if v := decodeView(t, rec); v.Status != "pending_second_approval" || v.ProposedBy != "officer-1" ||
			v.ProposedStatus != "confirmed" || !v.RequiresSecondApproval {
			t.Fatalf("unexpected view: %+v", v)
		}

		if rec := doAs(h.ResolveAlert, "officer-2", `{"disposition":"cleared"}`, a.ID()); rec.Code != http.StatusConflict {
			t.Errorf("re-resolve pending: status = %d, want 409", rec.Code)
		}
		if rec := doAs(h.ApproveAlert, "officer-1", "", a.ID()); rec.Code != http.StatusForbidden {
			t.Errorf("self-approval: status = %d, want 403", rec.Code)
		}

		rec = doAs(h.ApproveAlert, "officer-2", `{"note":"ok"}`, a.ID())
		if rec.Code != http.StatusOK {
			t.Fatalf("approve status = %d: %s", rec.Code, rec.Body.String())
		}
		if v := decodeView(t, rec); v.Status != "confirmed" || v.ResolvedBy != "officer-2" || v.ApprovalNote != "ok" {
			t.Errorf("unexpected view: %+v", v)
		}

		if rec := doAs(h.ApproveAlert, "officer-3", "", a.ID()); rec.Code != http.StatusConflict {
			t.Errorf("approve closed alert: status = %d, want 409", rec.Code)
		}
	})

	t.Run("second analyst rejects: alert back to open", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		h := newHandler(r, &fakeWatchlist{})
		_ = doAs(h.ResolveAlert, "officer-1", `{"disposition":"cleared"}`, a.ID())

		rec := doAs(h.RejectAlertProposal, "officer-2", `{"note":"falta evidencia"}`, a.ID())
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if v := decodeView(t, rec); v.Status != "open" || v.ProposedBy != "" {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("error mapping", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		h := newHandler(r, &fakeWatchlist{})
		cases := []struct {
			name string
			rec  *httptest.ResponseRecorder
			want int
		}{
			{"invalid body", doAs(h.ResolveAlert, "o", `{bad`, a.ID()), http.StatusBadRequest},
			{"invalid disposition", doAs(h.ResolveAlert, "o", `{"disposition":"maybe"}`, a.ID()), http.StatusBadRequest},
			{"not found", doAs(h.ResolveAlert, "o", `{"disposition":"cleared"}`, domain.NewAlertID()), http.StatusNotFound},
			{"approve without proposal", doAs(h.ApproveAlert, "o", "", a.ID()), http.StatusConflict},
			{"reject invalid body", doAs(h.RejectAlertProposal, "o", `{bad`, a.ID()), http.StatusBadRequest},
		}
		for _, c := range cases {
			if c.rec.Code != c.want {
				t.Errorf("%s: status = %d, want %d", c.name, c.rec.Code, c.want)
			}
		}
	})

	t.Run("concurrent review yields 409", func(t *testing.T) {
		r := newAlertRepo()
		a := seedAlert(r, tid)
		r.updateErr = domain.ErrAlertNotOpen
		rec := doAs(newHandler(r, &fakeWatchlist{}).ResolveAlert, "o", `{"disposition":"cleared"}`, a.ID())
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})
}

func TestPlatformOperatorMiddleware(t *testing.T) {
	// El middleware debe marcar el ctx para que RunWithAppTenant/RunInTx fijen
	// app.platform_operator='on' (política RLS cross-tenant de aml_alerts).
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock: %v", err)
	}
	defer mock.Close()
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs(postgres.PlatformOperatorKey, "on").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectCommit()

	rec := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(rec)
	engine.GET("/x", platformOperator, func(c *gin.Context) {
		_ = postgres.RunWithAppTenant(c.Request.Context(), mock, func(context.Context) error { return nil })
	})
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	engine.HandleContext(c)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("platform operator var not set: %v", err)
	}
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
			t.Fatalf("expected 1 entry added, got %d", len(w.added))
		}
		var view application.WatchlistEntryView
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.ID != w.added[0].ID {
			t.Errorf("expected created entry in body, got %s (%v)", rec.Body.String(), err)
		}
	})

	t.Run("invalid list_type yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"full_name":"x","list_type":"terrorist"}`, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
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

	t.Run("repo error yields 500 without leaking details", func(t *testing.T) {
		w := &fakeWatchlist{addErr: errors.New("pq: secret internal detail")}
		rec := doRequest(newHandler(newAlertRepo(), w).AddWatchlistEntry, http.MethodPost,
			"/compliance/watchlist", `{"full_name":"x","list_type":"pep"}`, "", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("internal error leaked: %s", rec.Body.String())
		}
	})
}

func TestRemoveWatchlistEntry(t *testing.T) {
	t.Run("removes and returns 204", func(t *testing.T) {
		w := &fakeWatchlist{}
		id := uuid.NewString()
		rec := doRequest(newHandler(newAlertRepo(), w).RemoveWatchlistEntry, http.MethodDelete,
			"/x", "", "", gin.Params{{Key: "entryID", Value: id}})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if len(w.removed) != 1 || w.removed[0] != id {
			t.Errorf("removed = %v", w.removed)
		}
	})

	t.Run("not found yields 404", func(t *testing.T) {
		w := &fakeWatchlist{removeErr: domain.ErrWatchlistEntryNotFound}
		rec := doRequest(newHandler(newAlertRepo(), w).RemoveWatchlistEntry, http.MethodDelete,
			"/x", "", "", gin.Params{{Key: "entryID", Value: uuid.NewString()}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid id yields 404", func(t *testing.T) {
		rec := doRequest(newHandler(newAlertRepo(), &fakeWatchlist{}).RemoveWatchlistEntry, http.MethodDelete,
			"/x", "", "", gin.Params{{Key: "entryID", Value: "bad"}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		w := &fakeWatchlist{removeErr: errors.New("boom")}
		rec := doRequest(newHandler(newAlertRepo(), w).RemoveWatchlistEntry, http.MethodDelete,
			"/x", "", "", gin.Params{{Key: "entryID", Value: uuid.NewString()}})
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func allow(c *gin.Context) { c.Next() }
func deny(c *gin.Context)  { c.AbortWithStatus(http.StatusForbidden) }

func TestRegisterRoutes(t *testing.T) {
	w := &fakeWatchlist{entries: []domain.WatchlistEntry{{ID: "1", FullName: "A", ListType: "pep"}}}
	all := func(g gin.HandlerFunc) RouteGuards {
		return RouteGuards{ReadAlerts: g, ReviewAlerts: g, ReadWatchlist: g, ManageWatchlist: g}
	}

	t.Run("routes are wired behind their guards", func(t *testing.T) {
		r := gin.New()
		RegisterRoutes(r.Group("/api/v1"), newHandler(newAlertRepo(), w), all(allow))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/compliance/watchlist", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/compliance/watchlist status = %d, want 200", rec.Code)
		}
	})

	t.Run("each route enforces its guard", func(t *testing.T) {
		r := gin.New()
		RegisterRoutes(r.Group("/api/v1"), newHandler(newAlertRepo(), w), all(deny))
		id := uuid.NewString()
		routes := []struct{ method, path, body string }{
			{http.MethodGet, "/api/v1/compliance/alerts", ""},
			{http.MethodGet, "/api/v1/compliance/alerts/" + id, ""},
			{http.MethodPost, "/api/v1/compliance/alerts/" + id + "/resolve", `{"disposition":"cleared"}`},
			{http.MethodPost, "/api/v1/compliance/alerts/" + id + "/approve", ""},
			{http.MethodPost, "/api/v1/compliance/alerts/" + id + "/reject", ""},
			{http.MethodGet, "/api/v1/compliance/watchlist", ""},
			{http.MethodPost, "/api/v1/compliance/watchlist", `{"full_name":"x","list_type":"pep"}`},
			{http.MethodDelete, "/api/v1/compliance/watchlist/" + id, ""},
			{http.MethodPost, "/api/v1/compliance/watchlist/import?source=OFAC", "external_ref,full_name,list_type\n1,x,pep\n"},
		}
		for _, rt := range routes {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body)))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s status = %d, want 403", rt.method, rt.path, rec.Code)
			}
		}
		if len(w.added) != 0 || len(w.removed) != 0 {
			t.Error("guarded handlers must not run")
		}
	})

	t.Run("missing guard panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic for missing guard")
			}
		}()
		g := all(allow)
		g.ReviewAlerts = nil
		RegisterRoutes(gin.New().Group("/api/v1"), newHandler(newAlertRepo(), w), g)
	})
}
