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

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	"github.com/juantevez/cobros-platform/context/reconciliation/infrastructure/adapters/outbound/readers"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

// testTenant es el tenant autenticado de los requests y dueño de los datos sembrados.
var testTenant = uuid.NewString()

// testActor es el usuario autenticado de los requests con tenant.
var testActor = uuid.NewString()

var (
	periodFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	periodTo   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeRunRepo struct {
	byID      map[domain.RunID]*domain.ReconciliationRun
	listed    []*domain.ReconciliationRun
	listErr   error
	lastLimit int
}

func (r *fakeRunRepo) Save(ctx context.Context, run *domain.ReconciliationRun) error {
	r.byID[run.ID()] = run
	return nil
}
func (r *fakeRunRepo) Update(ctx context.Context, run *domain.ReconciliationRun) error {
	return nil
}
func (r *fakeRunRepo) FindByID(ctx context.Context, id domain.RunID) (*domain.ReconciliationRun, error) {
	run, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrRunNotFound
	}
	return run, nil
}
func (r *fakeRunRepo) List(ctx context.Context, t domain.TenantID, limit int) ([]*domain.ReconciliationRun, error) {
	r.lastLimit = limit
	return r.listed, r.listErr
}

type fakeDiscRepo struct {
	byID       map[domain.DiscrepancyID]*domain.Discrepancy
	saved      []*domain.Discrepancy
	listed     []*domain.Discrepancy
	listErr    error
	lastFilter string
	lastLimit  int
}

func (r *fakeDiscRepo) SaveAll(ctx context.Context, ds []*domain.Discrepancy) error {
	r.saved = append(r.saved, ds...)
	return nil
}
func (r *fakeDiscRepo) Update(ctx context.Context, d *domain.Discrepancy) error { return nil }
func (r *fakeDiscRepo) FindByID(ctx context.Context, id domain.DiscrepancyID) (*domain.Discrepancy, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDiscrepancyNotFound
	}
	return d, nil
}
func (r *fakeDiscRepo) ListByRun(ctx context.Context, id domain.RunID, filter string, limit int) ([]*domain.Discrepancy, error) {
	r.lastFilter, r.lastLimit = filter, limit
	return r.listed, r.listErr
}

type fakePaymentReader struct{ payments []application.SystemPayment }

func (f fakePaymentReader) ReadByPeriod(ctx context.Context, t domain.TenantID, from, to time.Time) ([]application.SystemPayment, error) {
	return f.payments, nil
}

type fakeLedger struct{ check application.LedgerCheck }

func (f fakeLedger) Check(ctx context.Context, t domain.TenantID, from, to time.Time) (application.LedgerCheck, error) {
	return f.check, nil
}

type fakePublisher struct{}

func (fakePublisher) Publish(ctx context.Context, events ...domain.Event) error { return nil }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// ── Helpers ───────────────────────────────────────────────────────────────────

type env struct {
	runs  *fakeRunRepo
	discs *fakeDiscRepo
	h     *ReconciliationHandler
}

// newEnv arma el handler con fakes; unbalanced es la cantidad de asientos
// desbalanceados que informa el Ledger.
func newEnv(payments []application.SystemPayment, unbalanced int) *env {
	check := application.LedgerCheck{EntriesChecked: unbalanced}
	for i := 0; i < unbalanced; i++ {
		check.Unbalanced = append(check.Unbalanced, application.LedgerImbalance{EntryID: fmt.Sprint("E", i), Currency: "ARS", Imbalance: 1})
	}
	runs := &fakeRunRepo{byID: map[domain.RunID]*domain.ReconciliationRun{}}
	discs := &fakeDiscRepo{byID: map[domain.DiscrepancyID]*domain.Discrepancy{}}
	h := NewReconciliationHandler(
		application.NewStartReconciliationUseCase(runs, realClock{}),
		application.NewProcessReportUseCase(runs, discs, fakePaymentReader{payments}, readers.NewCSVReportParser(), fakeTx{}, fakePublisher{}, realClock{}),
		application.NewProcessInternalUseCase(runs, discs, fakeLedger{check}, fakeTx{}, fakePublisher{}, realClock{}),
		application.NewResolveDiscrepancyUseCase(discs, fakeTx{}, fakePublisher{}, realClock{}),
		application.NewGetReportUseCase(runs, discs),
		application.NewListRunsUseCase(runs),
	)
	return &env{runs: runs, discs: discs, h: h}
}

func (e *env) seedRun(t domain.ReconciliationType) *domain.ReconciliationRun {
	run, _ := domain.NewReconciliationRun(domain.NewRunID(), domain.TenantID(testTenant), t, periodFrom, periodTo, time.Now())
	e.runs.byID[run.ID()] = run
	return run
}

func (e *env) seedDiscrepancy() *domain.Discrepancy {
	d := domain.NewDiscrepancy(domain.NewDiscrepancyID(), domain.NewRunID(), domain.TenantID(testTenant), domain.DiscrepancyAmountMismatch, "PSP-1", "100 ARS", "90 ARS", time.Now())
	e.discs.byID[d.ID()] = d
	return d
}

func doRequest(h gin.HandlerFunc, method, target, body, tenantID string, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if tenantID != "" {
		req = req.WithContext(postgres.WithActor(postgres.WithTenantID(req.Context(), tenantID), testActor))
	}
	c.Request = req
	c.Params = params
	h(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func runParam(id string) gin.Params  { return gin.Params{{Key: "runID", Value: id}} }
func discParam(id string) gin.Params { return gin.Params{{Key: "discrepancyID", Value: id}} }

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

// ── StartRun ──────────────────────────────────────────────────────────────────

func TestStartRun(t *testing.T) {
	tid := uuid.NewString()

	t.Run("creates run returns 201 with run_id", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := doRequest(e.h.StartRun, http.MethodPost, "/runs",
			`{"type":"payment","period_from":"2026-09-01T00:00:00Z","period_to":"2026-10-01T00:00:00Z"}`, tid, nil)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var body struct {
			RunID string `json:"run_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		run := e.runs.byID[domain.RunID(body.RunID)]
		if run == nil {
			t.Fatalf("run %q not saved", body.RunID)
		}
		if run.TenantID() != domain.TenantID(tid) || run.Type() != domain.TypePayment || !run.PeriodFrom().Equal(periodFrom) {
			t.Errorf("run not built from request: tenant=%q type=%q from=%v", run.TenantID(), run.Type(), run.PeriodFrom())
		}
	})

	badRequests := []struct{ name, body, wantErr string }{
		{"malformed json", `{bad`, "invalid request body"},
		{"missing type", `{"period_from":"2026-09-01T00:00:00Z","period_to":"2026-10-01T00:00:00Z"}`, "invalid request body"},
		{"missing period_to", `{"type":"payment","period_from":"2026-09-01T00:00:00Z"}`, "invalid request body"},
		{"non RFC3339 period_from", `{"type":"payment","period_from":"2026-09-01","period_to":"2026-10-01T00:00:00Z"}`, "invalid period_from: use RFC3339"},
		{"non RFC3339 period_to", `{"type":"payment","period_from":"2026-09-01T00:00:00Z","period_to":"tomorrow"}`, "invalid period_to: use RFC3339"},
		{"inverted period", `{"type":"payment","period_from":"2026-10-01T00:00:00Z","period_to":"2026-09-01T00:00:00Z"}`, domain.ErrInvalidPeriod.Error()},
	}
	for _, tc := range badRequests {
		t.Run("400 on "+tc.name, func(t *testing.T) {
			e := newEnv(nil, 0)
			rec := doRequest(e.h.StartRun, http.MethodPost, "/runs", tc.body, tid, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorBody(t, rec); got != tc.wantErr {
				t.Errorf("error = %q, want %q", got, tc.wantErr)
			}
			if len(e.runs.byID) != 0 {
				t.Error("nothing should be saved")
			}
		})
	}
}

// ── ListRuns ──────────────────────────────────────────────────────────────────

func TestListRuns(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns runs with count", func(t *testing.T) {
		e := newEnv(nil, 0)
		e.runs.listed = []*domain.ReconciliationRun{e.seedRun(domain.TypePayment), e.seedRun(domain.TypeInternal)}

		rec := doRequest(e.h.ListRuns, http.MethodGet, "/runs?limit=7", "", tid, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body struct {
			Runs  []application.RunView `json:"runs"`
			Count int                   `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if body.Count != 2 || len(body.Runs) != 2 || e.runs.lastLimit != 7 {
			t.Errorf("count=%d len=%d limit=%d", body.Count, len(body.Runs), e.runs.lastLimit)
		}
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		e := newEnv(nil, 0)
		e.runs.listErr = errBoom
		rec := doRequest(e.h.ListRuns, http.MethodGet, "/runs", "", tid, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	for _, q := range []string{"/runs", "/runs?limit=abc"} {
		t.Run("default limit for "+q, func(t *testing.T) {
			e := newEnv(nil, 0)
			doRequest(e.h.ListRuns, http.MethodGet, q, "", tid, nil)
			if e.runs.lastLimit != 20 {
				t.Errorf("limit = %d, want 20", e.runs.lastLimit)
			}
		})
	}
}

// ── GetReport ─────────────────────────────────────────────────────────────────

func TestGetReport(t *testing.T) {
	t.Run("returns report and forwards filters", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		e.discs.listed = []*domain.Discrepancy{e.seedDiscrepancy()}

		rec := doRequest(e.h.GetReport, http.MethodGet, "/runs/x?status=open&limit=5", "", testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var rep application.ReportView
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("invalid body: %v", err)
		}
		if rep.Run.ID != run.ID().String() || len(rep.Discrepancies) != 1 || rep.Discrepancies[0].RecordID != "PSP-1" {
			t.Errorf("unexpected report: %+v", rep)
		}
		if e.discs.lastFilter != "open" || e.discs.lastLimit != 5 {
			t.Errorf("filter=%q limit=%d", e.discs.lastFilter, e.discs.lastLimit)
		}
	})

	t.Run("default limit is 100", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		doRequest(e.h.GetReport, http.MethodGet, "/runs/x", "", testTenant, runParam(run.ID().String()))
		if e.discs.lastLimit != 100 {
			t.Errorf("limit = %d, want 100", e.discs.lastLimit)
		}
	})

	t.Run("unknown run returns 404", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := doRequest(e.h.GetReport, http.MethodGet, "/runs/x", "", testTenant, runParam(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("list error returns 500", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		e.discs.listErr = errBoom
		rec := doRequest(e.h.GetReport, http.MethodGet, "/runs/x", "", testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── UploadReport ──────────────────────────────────────────────────────────────

func TestUploadReport(t *testing.T) {
	csv := "transaction_id,amount,currency,status\nPSP-1,100,ARS,captured\nPSP-2,50,ARS,captured\n"

	t.Run("processes CSV and returns 202", func(t *testing.T) {
		e := newEnv([]application.SystemPayment{{PaymentID: "p1", PSPReference: "PSP-1", Amount: 100, Currency: "ARS", Status: "captured"}}, 0)
		run := e.seedRun(domain.TypePayment)

		rec := doRequest(e.h.UploadReport, http.MethodPost, "/runs/x/report", csv, testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
		}
		if run.Status() != domain.RunStatusCompleted || run.MatchedCount() != 1 || run.DiscrepancyCount() != 1 {
			t.Errorf("status=%q matched=%d discrepancies=%d", run.Status(), run.MatchedCount(), run.DiscrepancyCount())
		}
		if len(e.discs.saved) != 1 || e.discs.saved[0].Type() != domain.DiscrepancyMissingInSystem {
			t.Errorf("saved = %v", e.discs.saved)
		}
	})

	t.Run("empty body returns 400", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		rec := doRequest(e.h.UploadReport, http.MethodPost, "/runs/x/report", "", testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := errorBody(t, rec); got != "report data is empty" {
			t.Errorf("error = %q", got)
		}
		if run.Status() != domain.RunStatusPending {
			t.Error("run should not be touched")
		}
	})

	for name, csv := range map[string]string{
		"missing columns": "id,amount\nA,1\n",
		"decimal amount":  "transaction_id,amount,currency,status\nA,12.50,ARS,captured\n",
	} {
		t.Run("invalid CSV ("+name+") returns 400 and leaves the run untouched", func(t *testing.T) {
			e := newEnv(nil, 0)
			run := e.seedRun(domain.TypePayment)
			rec := doRequest(e.h.UploadReport, http.MethodPost, "/runs/x/report", csv, testTenant, runParam(run.ID().String()))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
			if run.Status() != domain.RunStatusPending {
				t.Errorf("status = %q, want pending (reprocesable con un CSV corregido)", run.Status())
			}
		})
	}

	t.Run("unknown run returns 404", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := doRequest(e.h.UploadReport, http.MethodPost, "/runs/x/report", csv, testTenant, runParam(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("already processed run returns 409", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		_ = run.Start(time.Now())
		rec := doRequest(e.h.UploadReport, http.MethodPost, "/runs/x/report", csv, testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("unreadable body returns 400", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/runs/x/report", errReader{})
		c.Request = c.Request.WithContext(postgres.WithTenantID(c.Request.Context(), testTenant))
		c.Params = runParam(uuid.NewString())

		e.h.UploadReport(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := errorBody(t, rec); got != "cannot read request body" {
			t.Errorf("error = %q", got)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBoom }

// ── ProcessInternal ───────────────────────────────────────────────────────────

func TestProcessInternal(t *testing.T) {
	t.Run("runs ledger check and returns 202", func(t *testing.T) {
		e := newEnv(nil, 1)
		run := e.seedRun(domain.TypeInternal)

		rec := doRequest(e.h.ProcessInternal, http.MethodPost, "/runs/x/process-internal", "", testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
		}
		if run.Status() != domain.RunStatusCompleted || len(e.discs.saved) != 1 {
			t.Errorf("status=%q saved=%d", run.Status(), len(e.discs.saved))
		}
	})

	t.Run("unknown run returns 404", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := doRequest(e.h.ProcessInternal, http.MethodPost, "/x", "", testTenant, runParam(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("already processed run returns 409", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypeInternal)
		_ = run.Start(time.Now())
		_ = run.Complete(1, 1, 0, time.Now())
		rec := doRequest(e.h.ProcessInternal, http.MethodPost, "/x", "", testTenant, runParam(run.ID().String()))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})
}

// ── ResolveDiscrepancy ────────────────────────────────────────────────────────

func TestResolveDiscrepancy(t *testing.T) {
	t.Run("resolves as the authenticated user and returns 204", func(t *testing.T) {
		e := newEnv(nil, 0)
		d := e.seedDiscrepancy()

		// resolved_by en el body se ignora: firma el actor autenticado.
		rec := doRequest(e.h.ResolveDiscrepancy, http.MethodPost, "/x",
			`{"action":"ignore","resolved_by":"someone-else","notes":"known"}`, testTenant, discParam(d.ID().String()))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
		}
		if d.Status() != domain.DiscrepancyIgnored || d.ResolvedBy() != testActor || d.Notes() != "known" {
			t.Errorf("status=%q by=%q notes=%q", d.Status(), d.ResolvedBy(), d.Notes())
		}
	})

	for _, body := range []string{`{bad`, `{"notes":"x"}`} {
		t.Run("400 on "+body, func(t *testing.T) {
			e := newEnv(nil, 0)
			d := e.seedDiscrepancy()
			rec := doRequest(e.h.ResolveDiscrepancy, http.MethodPost, "/x", body, testTenant, discParam(d.ID().String()))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}

	t.Run("unknown discrepancy returns 404", func(t *testing.T) {
		e := newEnv(nil, 0)
		rec := doRequest(e.h.ResolveDiscrepancy, http.MethodPost, "/x",
			`{"action":"resolve","resolved_by":"op"}`, testTenant, discParam(uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("already resolved returns 409", func(t *testing.T) {
		e := newEnv(nil, 0)
		d := e.seedDiscrepancy()
		_ = d.Resolve("someone", "", time.Now())
		rec := doRequest(e.h.ResolveDiscrepancy, http.MethodPost, "/x",
			`{"action":"resolve","resolved_by":"op"}`, testTenant, discParam(d.ID().String()))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})
}

// ── Aislamiento por tenant ────────────────────────────────────────────────────

func TestMissingTenantReturns401(t *testing.T) {
	e := newEnv(nil, 0)
	run := e.seedRun(domain.TypePayment)
	d := e.seedDiscrepancy()

	cases := []struct {
		name   string
		h      gin.HandlerFunc
		body   string
		params gin.Params
	}{
		{"StartRun", e.h.StartRun, `{"type":"payment","period_from":"2026-09-01T00:00:00Z","period_to":"2026-10-01T00:00:00Z"}`, nil},
		{"ListRuns", e.h.ListRuns, "", nil},
		{"GetReport", e.h.GetReport, "", runParam(run.ID().String())},
		{"UploadReport", e.h.UploadReport, "transaction_id,amount,currency,status\nA,1,ARS,captured\n", runParam(run.ID().String())},
		{"ProcessInternal", e.h.ProcessInternal, "", runParam(run.ID().String())},
		{"ResolveDiscrepancy", e.h.ResolveDiscrepancy, `{"action":"resolve","resolved_by":"op"}`, discParam(d.ID().String())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(tc.h, http.MethodPost, "/x", tc.body, "", tc.params)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := errorBody(t, rec); got != "missing tenant" {
				t.Errorf("error = %q", got)
			}
		})
	}

	if len(e.runs.byID) != 1 || run.Status() != domain.RunStatusPending || d.Status() != domain.DiscrepancyOpen {
		t.Error("requests without tenant must not change anything")
	}
}

func TestOtherTenantGets404(t *testing.T) {
	other := uuid.NewString()
	csv := "transaction_id,amount,currency,status\nA,1,ARS,captured\n"

	t.Run("GetReport", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		rec := doRequest(e.h.GetReport, http.MethodGet, "/x", "", other, runParam(run.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("UploadReport", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypePayment)
		rec := doRequest(e.h.UploadReport, http.MethodPost, "/x", csv, other, runParam(run.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if run.Status() != domain.RunStatusPending {
			t.Errorf("foreign run was processed: %q", run.Status())
		}
	})

	t.Run("ProcessInternal", func(t *testing.T) {
		e := newEnv(nil, 0)
		run := e.seedRun(domain.TypeInternal)
		rec := doRequest(e.h.ProcessInternal, http.MethodPost, "/x", "", other, runParam(run.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if run.Status() != domain.RunStatusPending {
			t.Errorf("foreign run was processed: %q", run.Status())
		}
	})

	t.Run("ResolveDiscrepancy", func(t *testing.T) {
		e := newEnv(nil, 0)
		d := e.seedDiscrepancy()
		rec := doRequest(e.h.ResolveDiscrepancy, http.MethodPost, "/x", `{"action":"resolve","resolved_by":"op"}`, other, discParam(d.ID().String()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if d.Status() != domain.DiscrepancyOpen {
			t.Errorf("foreign discrepancy was resolved: %q", d.Status())
		}
	})
}

// ── Errores de cliente ────────────────────────────────────────────────────────

func TestClientErrorsReturn400(t *testing.T) {
	e := newEnv(nil, 0)
	run := e.seedRun(domain.TypePayment)
	d := e.seedDiscrepancy()

	cases := []struct {
		name   string
		h      gin.HandlerFunc
		target string
		body   string
		params gin.Params
	}{
		{"invalid run id", e.h.GetReport, "/x", "", runParam("not-a-uuid")},
		{"invalid status filter", e.h.GetReport, "/x?status=closed", "", runParam(run.ID().String())},
		{"invalid run type", e.h.StartRun, "/x", `{"type":"payout","period_from":"2026-09-01T00:00:00Z","period_to":"2026-10-01T00:00:00Z"}`, nil},
		{"invalid discrepancy id", e.h.ResolveDiscrepancy, "/x", `{"action":"resolve"}`, discParam("nope")},
		{"invalid action", e.h.ResolveDiscrepancy, "/x", `{"action":"delete"}`, discParam(d.ID().String())},
		{"invalid run id on upload", e.h.UploadReport, "/x", "transaction_id,amount,currency,status\nA,1,ARS,captured\n", runParam("nope")},
		{"invalid run id on process-internal", e.h.ProcessInternal, "/x", "", runParam("nope")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(tc.h, http.MethodPost, tc.target, tc.body, testTenant, tc.params)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// ── respondError ──────────────────────────────────────────────────────────────

func TestRespondError(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{domain.ErrRunNotFound, http.StatusNotFound},
		{domain.ErrDiscrepancyNotFound, http.StatusNotFound},
		{domain.ErrRunAlreadyRunning, http.StatusConflict},
		{domain.ErrDiscrepancyAlreadyResolved, http.StatusConflict},
		{domain.ErrInvalidPeriod, http.StatusBadRequest},
		{domain.ErrEmptyReport, http.StatusBadRequest},
		{domain.ErrInvalidReportFormat, http.StatusBadRequest},
		{domain.ErrRunTypeMismatch, http.StatusUnprocessableEntity},
		{domain.ErrInvalidArgument, http.StatusBadRequest},
		{fmt.Errorf("find run: %w", domain.ErrRunNotFound), http.StatusNotFound},
		{domain.ErrRunNotRunning, http.StatusInternalServerError},
		{errBoom, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			respondError(c, tc.err)

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

// guard registra el nombre del guard que atendió el request y lo corta.
func guard(name string) gin.HandlerFunc {
	return func(c *gin.Context) { c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"guard": name}) }
}

func TestRegisterRoutes(t *testing.T) {
	engine := gin.New()
	RegisterRoutes(engine.Group("/api/v1"), newEnv(nil, 0).h, RouteGuards{Read: guard("read"), Operate: guard("operate")})

	want := map[string]string{
		"GET /api/v1/reconciliation/runs":                                  "read",
		"GET /api/v1/reconciliation/runs/:runID":                           "read",
		"POST /api/v1/reconciliation/discrepancies/:discrepancyID/resolve": "operate",
		"POST /api/v1/reconciliation/runs":                                 "operate",
		"POST /api/v1/reconciliation/runs/:runID/process-internal":         "operate",
		"POST /api/v1/reconciliation/runs/:runID/report":                   "operate",
	}
	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	if len(got) != len(want) {
		t.Fatalf("routes = %v", got)
	}

	for route, wantGuard := range want {
		method, path, _ := strings.Cut(route, " ")
		path = strings.NewReplacer(":runID", uuid.NewString(), ":discrepancyID", uuid.NewString()).Replace(path)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		var body struct{ Guard string }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || body.Guard != wantGuard {
			t.Errorf("%s: guard = %q (status %d), want %q", route, body.Guard, rec.Code, wantGuard)
		}
	}
}

func TestRegisterRoutes_PanicsWithoutGuards(t *testing.T) {
	for name, g := range map[string]RouteGuards{
		"no read":    {Operate: guard("operate")},
		"no operate": {Read: guard("read")},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			RegisterRoutes(gin.New().Group("/"), newEnv(nil, 0).h, g)
		})
	}
}
