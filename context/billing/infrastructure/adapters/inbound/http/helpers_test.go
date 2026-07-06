package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/billing/application"
	"github.com/juantevez/cobros-platform/context/billing/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ── Fakes de los puertos del contexto Billing ─────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakePlanRepo struct {
	byID    map[domain.PlanID]*domain.PricingPlan
	saveErr error
}

func newFakePlanRepo(plans ...*domain.PricingPlan) *fakePlanRepo {
	m := map[domain.PlanID]*domain.PricingPlan{}
	for _, p := range plans {
		m[p.ID()] = p
	}
	return &fakePlanRepo{byID: m}
}

func (r *fakePlanRepo) Save(ctx context.Context, p *domain.PricingPlan) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[p.ID()] = p
	return nil
}
func (r *fakePlanRepo) Update(ctx context.Context, p *domain.PricingPlan) error {
	r.byID[p.ID()] = p
	return nil
}
func (r *fakePlanRepo) FindByID(ctx context.Context, id domain.PlanID) (*domain.PricingPlan, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPlanNotFound
	}
	return p, nil
}
func (r *fakePlanRepo) ListActive(ctx context.Context) ([]*domain.PricingPlan, error) {
	var out []*domain.PricingPlan
	for _, p := range r.byID {
		if p.Active() {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeTenantPlanRepo struct {
	active map[domain.TenantID]*domain.TenantPlan
}

func newFakeTenantPlanRepo() *fakeTenantPlanRepo {
	return &fakeTenantPlanRepo{active: map[domain.TenantID]*domain.TenantPlan{}}
}

func (r *fakeTenantPlanRepo) Save(ctx context.Context, tp *domain.TenantPlan) error {
	if tp.Active() {
		r.active[tp.TenantID()] = tp
	}
	return nil
}
func (r *fakeTenantPlanRepo) Update(ctx context.Context, tp *domain.TenantPlan) error {
	if !tp.Active() {
		delete(r.active, tp.TenantID())
	}
	return nil
}
func (r *fakeTenantPlanRepo) FindActive(ctx context.Context, tenantID domain.TenantID) (*domain.TenantPlan, error) {
	tp, ok := r.active[tenantID]
	if !ok {
		return nil, domain.ErrTenantPlanNotFound
	}
	return tp, nil
}
func (r *fakeTenantPlanRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.TenantPlan, error) {
	if tp, ok := r.active[tenantID]; ok {
		return []*domain.TenantPlan{tp}, nil
	}
	return nil, nil
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

// ── testEnv ───────────────────────────────────────────────────────────────────

type testEnv struct {
	plans       *fakePlanRepo
	tenantPlans *fakeTenantPlanRepo
	pub         *fakePublisher
	engine      *gin.Engine
	tenantID    domain.TenantID
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	plans := newFakePlanRepo()
	tenantPlans := newFakeTenantPlanRepo()
	pub := &fakePublisher{}
	tx := fakeTx{}

	createPlan := application.NewCreatePlanUseCase(plans, tx, pub)
	assignPlan := application.NewAssignPlanUseCase(plans, tenantPlans, tx, pub)
	getPlan := application.NewGetPlanUseCase(plans)
	listPlans := application.NewListPlansUseCase(plans)
	getTenantPlan := application.NewGetTenantPlanUseCase(tenantPlans)

	tenantID, err := domain.ParseTenantID(uuid.NewString())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}

	r := gin.New()
	grp := r.Group("/api/v1")
	grp.Use(func(c *gin.Context) {
		ctx := postgres.WithTenantID(c.Request.Context(), tenantID.String())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterRoutes(grp, NewBillingHandler(createPlan, assignPlan, getPlan, listPlans, getTenantPlan))

	return &testEnv{
		plans: plans, tenantPlans: tenantPlans, pub: pub,
		engine: r, tenantID: tenantID,
	}
}

func (e *testEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func timeNow() time.Time { return time.Now().UTC() }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// seedPlan crea un plan activo directamente en el fake repo.
func (e *testEnv) seedPlan(t *testing.T, name string, baseRateBps int64) *domain.PricingPlan {
	t.Helper()
	p, err := domain.NewPricingPlan(domain.NewPlanID(), name, "desc", baseRateBps, 0, 0, "ARS")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	p.PullEvents()
	e.plans.byID[p.ID()] = p
	return p
}
