package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/onboarding/application"
	"github.com/juantevez/cobros-platform/context/onboarding/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ── Fakes de puertos de application ───────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeRepo struct {
	byID     map[domain.ApplicationID]*domain.OnboardingApplication
	byTenant map[domain.TenantID]*domain.OnboardingApplication
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
	r.byID[app.ID()] = app
	r.byTenant[app.TenantID()] = app
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, app *domain.OnboardingApplication) error { return nil }
func (r *fakeRepo) FindByID(ctx context.Context, id domain.ApplicationID) (*domain.OnboardingApplication, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrApplicationNotFound
	}
	return a, nil
}
func (r *fakeRepo) FindByTenantID(ctx context.Context, tid domain.TenantID) (*domain.OnboardingApplication, error) {
	a, ok := r.byTenant[tid]
	if !ok {
		return nil, domain.ErrApplicationNotFound
	}
	return a, nil
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

// ── Builders de agregados ─────────────────────────────────────────────────────

func completeInfo() domain.BusinessInfo {
	return domain.BusinessInfo{
		LegalName:        "Acme SA",
		TaxID:            domain.TaxID("20304050607"),
		BusinessCategory: domain.CategoryRetail,
		Address:          domain.Address{Street: "Calle 1", City: "CABA", Country: "AR"},
	}
}

func pendingApp(t *testing.T, tid domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a, err := domain.NewOnboardingApplication(domain.NewApplicationID(), tid, completeInfo())
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	a.PullEvents()
	return a
}

func completePendingApp(t *testing.T, tid domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a := pendingApp(t, tid)
	_ = a.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypeIDCard, "s3://ref"))
	_ = a.AddPerson(domain.NewPerson(domain.NewPersonID(), "Owner", domain.RoleOwner, "DNI", "1", "AR"))
	_ = a.SetBankAccount(domain.NewBankAccount(domain.NewBankAccountID(), domain.BankAccountCBU, "1", "B", "A", "ARS"))
	return a
}

func inReviewApp(t *testing.T, tid domain.TenantID) *domain.OnboardingApplication {
	t.Helper()
	a := completePendingApp(t, tid)
	if err := a.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	a.PullEvents()
	return a
}

// ── testEnv ───────────────────────────────────────────────────────────────────

type testEnv struct {
	repo     *fakeRepo
	engine   *gin.Engine
	tenantID domain.TenantID
}

func newTestEnv(t *testing.T, apps ...*domain.OnboardingApplication) *testEnv {
	t.Helper()
	repo := newFakeRepo(apps...)
	pub := &fakePublisher{}
	tx := fakeTx{}

	submit := application.NewSubmitApplicationUseCase(repo, tx, pub)
	uploadDoc := application.NewUploadDocumentUseCase(repo, tx)
	addPerson := application.NewAddPersonUseCase(repo, tx)
	setBank := application.NewSetBankAccountUseCase(repo, tx)
	submitReview := application.NewSubmitForReviewUseCase(repo, tx, pub)
	getApp := application.NewGetApplicationUseCase(repo)
	review := application.NewReviewApplicationUseCase(repo, tx, pub)

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
	RegisterRoutes(grp,
		NewOnboardingHandler(submit, uploadDoc, addPerson, setBank, submitReview, getApp),
		NewReviewHandler(review),
	)

	return &testEnv{repo: repo, engine: r, tenantID: tenantID}
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

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func newTestCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}
