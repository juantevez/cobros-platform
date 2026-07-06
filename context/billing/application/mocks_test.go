package application

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// mocks_test.go: test doubles in-memory de los puertos del contexto Billing.

// ── TxManager ─────────────────────────────────────────────────────────────────

type fakeTx struct{ err error }

func (t fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if t.err != nil {
		return t.err
	}
	return fn(ctx)
}

// ── PlanRepository ────────────────────────────────────────────────────────────

type fakePlanRepo struct {
	byID       map[domain.PlanID]*domain.PricingPlan
	saved      *domain.PricingPlan
	updated    *domain.PricingPlan
	saveErr    error
	updateErr  error
	findErr    error
	listErr    error
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
	r.saved = p
	r.byID[p.ID()] = p
	return nil
}

func (r *fakePlanRepo) Update(ctx context.Context, p *domain.PricingPlan) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = p
	r.byID[p.ID()] = p
	return nil
}

func (r *fakePlanRepo) FindByID(ctx context.Context, id domain.PlanID) (*domain.PricingPlan, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPlanNotFound
	}
	return p, nil
}

func (r *fakePlanRepo) ListActive(ctx context.Context) ([]*domain.PricingPlan, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []*domain.PricingPlan
	for _, p := range r.byID {
		if p.Active() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ── TenantPlanRepository ──────────────────────────────────────────────────────

type fakeTenantPlanRepo struct {
	active        map[domain.TenantID]*domain.TenantPlan
	saved         *domain.TenantPlan
	updated       *domain.TenantPlan
	saveErr       error
	updateErr     error
	findActiveErr error
}

func newFakeTenantPlanRepo() *fakeTenantPlanRepo {
	return &fakeTenantPlanRepo{active: map[domain.TenantID]*domain.TenantPlan{}}
}

func (r *fakeTenantPlanRepo) Save(ctx context.Context, tp *domain.TenantPlan) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = tp
	if tp.Active() {
		r.active[tp.TenantID()] = tp
	}
	return nil
}

func (r *fakeTenantPlanRepo) Update(ctx context.Context, tp *domain.TenantPlan) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = tp
	if !tp.Active() {
		delete(r.active, tp.TenantID())
	}
	return nil
}

func (r *fakeTenantPlanRepo) FindActive(ctx context.Context, tenantID domain.TenantID) (*domain.TenantPlan, error) {
	if r.findActiveErr != nil {
		return nil, r.findActiveErr
	}
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

// ── EventPublisher ────────────────────────────────────────────────────────────

type fakePublisher struct {
	published []domain.Event
	err       error
}

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, events...)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func validUUID() string { return uuid.NewString() }

func timeNowUTC() time.Time { return time.Now().UTC() }

// buildPlan crea un PricingPlan activo listo para usar en tests.
func buildPlan(t *testing.T, name string, baseRateBps, baseFixed int64) *domain.PricingPlan {
	t.Helper()
	p, err := domain.NewPricingPlan(domain.NewPlanID(), name, "desc", baseRateBps, baseFixed, 0, "ARS")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	p.PullEvents()
	return p
}

// buildTenantPlan asigna un plan a un tenant sin overrides.
func buildTenantPlan(t *testing.T, tenantID domain.TenantID, plan *domain.PricingPlan) *domain.TenantPlan {
	t.Helper()
	tp, err := domain.NewTenantPlan(domain.NewTenantPlanID(), tenantID, plan, -1, -1, time.Now().UTC())
	if err != nil {
		t.Fatalf("build tenant plan: %v", err)
	}
	tp.PullEvents()
	return tp
}
