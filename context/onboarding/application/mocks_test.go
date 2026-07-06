package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

var errBoom = errors.New("boom")

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeRepo struct {
	byID          map[domain.ApplicationID]*domain.OnboardingApplication
	byTenant      map[domain.TenantID]*domain.OnboardingApplication
	saved         *domain.OnboardingApplication
	updated       *domain.OnboardingApplication
	saveErr       error
	updateErr     error
	findByTenErr  error
	findByIDErr   error
}

func newFakeRepo(apps ...*domain.OnboardingApplication) *fakeRepo {
	r := &fakeRepo{
		byID:     map[domain.ApplicationID]*domain.OnboardingApplication{},
		byTenant: map[domain.TenantID]*domain.OnboardingApplication{},
	}
	for _, a := range apps {
		r.byID[a.ID()] = a
		r.byTenant[a.TenantID()] = a
	}
	return r
}

func (r *fakeRepo) Save(ctx context.Context, app *domain.OnboardingApplication) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = app
	r.byID[app.ID()] = app
	r.byTenant[app.TenantID()] = app
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, app *domain.OnboardingApplication) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = app
	return nil
}
func (r *fakeRepo) FindByID(ctx context.Context, id domain.ApplicationID) (*domain.OnboardingApplication, error) {
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrApplicationNotFound
	}
	return a, nil
}
func (r *fakeRepo) FindByTenantID(ctx context.Context, tid domain.TenantID) (*domain.OnboardingApplication, error) {
	if r.findByTenErr != nil {
		return nil, r.findByTenErr
	}
	a, ok := r.byTenant[tid]
	if !ok {
		return nil, domain.ErrApplicationNotFound
	}
	return a, nil
}

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

// ── Helpers de construcción de agregados ──────────────────────────────────────

func validUUID() string { return uuid.NewString() }

func testTenantID(t *testing.T) domain.TenantID {
	t.Helper()
	id, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return id
}

func completeInfo() domain.BusinessInfo {
	return domain.BusinessInfo{
		LegalName:        "Acme SA",
		TaxID:            domain.TaxID("20304050607"),
		BusinessCategory: domain.CategoryRetail,
		Address:          domain.Address{Street: "Calle 1", City: "CABA", Country: "AR"},
	}
}

// pendingApp: aplicación en estado pending (editable), sin documentos aún.
func pendingApp(t *testing.T, tenantID domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a, err := domain.NewOnboardingApplication(domain.NewApplicationID(), tenantID, completeInfo())
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	a.PullEvents()
	return a
}

// completePendingApp: pending con documento, persona y cuenta bancaria (lista para review).
func completePendingApp(t *testing.T, tenantID domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a := pendingApp(t, tenantID)
	mustAddCompleteness(t, a)
	return a
}

// inReviewApp: aplicación completa ya enviada a revisión (in_review).
func inReviewApp(t *testing.T, tenantID domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a := completePendingApp(t, tenantID)
	if err := a.SubmitForReview(); err != nil {
		t.Fatalf("submit for review: %v", err)
	}
	a.PullEvents()
	return a
}

func mustAddCompleteness(t *testing.T, a *domain.OnboardingApplication) {
	t.Helper()
	if err := a.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypeIDCard, "s3://ref")); err != nil {
		t.Fatalf("add doc: %v", err)
	}
	if err := a.AddPerson(domain.NewPerson(domain.NewPersonID(), "Owner", domain.RoleOwner, "DNI", "1", "AR")); err != nil {
		t.Fatalf("add person: %v", err)
	}
	if err := a.SetBankAccount(domain.NewBankAccount(domain.NewBankAccountID(), domain.BankAccountCBU, "1", "Banco", "Acme", "ARS")); err != nil {
		t.Fatalf("set bank: %v", err)
	}
}
