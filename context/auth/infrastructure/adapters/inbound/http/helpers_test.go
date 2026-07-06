package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// helpers_test.go define test doubles de los puertos de application y utilidades
// para manejar requests HTTP reales contra el router de Auth.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeTenantRepo struct {
	tenants   map[domain.TenantID]*domain.Tenant
	findErr   error
	updateErr error
	saveErr   error
}

func newFakeTenantRepo(ts ...*domain.Tenant) *fakeTenantRepo {
	m := map[domain.TenantID]*domain.Tenant{}
	for _, t := range ts {
		m[t.ID()] = t
	}
	return &fakeTenantRepo{tenants: m}
}

func (r *fakeTenantRepo) Save(ctx context.Context, t *domain.Tenant) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.tenants[t.ID()] = t
	return nil
}
func (r *fakeTenantRepo) Update(ctx context.Context, t *domain.Tenant) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.tenants[t.ID()] = t
	return nil
}
func (r *fakeTenantRepo) FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	t, ok := r.tenants[id]
	if !ok {
		return nil, domain.ErrTenantNotFound
	}
	return t, nil
}

type fakeUserRepo struct {
	byID    map[domain.UserID]*domain.User
	byEmail map[string]*domain.User
	saveErr error
}

func newFakeUserRepo(us ...*domain.User) *fakeUserRepo {
	r := &fakeUserRepo{byID: map[domain.UserID]*domain.User{}, byEmail: map[string]*domain.User{}}
	for _, u := range us {
		r.byID[u.ID()] = u
		r.byEmail[u.TenantID().String()+"|"+u.Email().String()] = u
	}
	return r
}

func (r *fakeUserRepo) Save(ctx context.Context, u *domain.User) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[u.ID()] = u
	return nil
}
func (r *fakeUserRepo) Update(ctx context.Context, u *domain.User) error { r.byID[u.ID()] = u; return nil }
func (r *fakeUserRepo) FindByID(ctx context.Context, id domain.UserID) (*domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return u, nil
}
func (r *fakeUserRepo) FindByEmail(ctx context.Context, tid domain.TenantID, email domain.Email) (*domain.User, error) {
	u, ok := r.byEmail[tid.String()+"|"+email.String()]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return u, nil
}

type fakeMembershipRepo struct {
	items map[string]domain.Membership
}

func newFakeMembershipRepo(ms ...domain.Membership) *fakeMembershipRepo {
	r := &fakeMembershipRepo{items: map[string]domain.Membership{}}
	for _, m := range ms {
		r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	}
	return r
}

func (r *fakeMembershipRepo) Save(ctx context.Context, m domain.Membership) error {
	r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	return nil
}
func (r *fakeMembershipRepo) Update(ctx context.Context, m domain.Membership) error {
	r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	return nil
}
func (r *fakeMembershipRepo) FindByUserAndTenant(ctx context.Context, uid domain.UserID, tid domain.TenantID) (*domain.Membership, error) {
	m, ok := r.items[uid.String()+"|"+tid.String()]
	if !ok {
		return nil, domain.ErrMembershipNotFound
	}
	return &m, nil
}
func (r *fakeMembershipRepo) ListByTenant(ctx context.Context, tid domain.TenantID) ([]domain.Membership, error) {
	return nil, nil
}

type fakeApiKeyRepo struct {
	byID      map[domain.ApiKeyID]*domain.ApiKey
	byPrefix  map[string]*domain.ApiKey
	findErr   error
}

func newFakeApiKeyRepo(keys ...*domain.ApiKey) *fakeApiKeyRepo {
	r := &fakeApiKeyRepo{byID: map[domain.ApiKeyID]*domain.ApiKey{}, byPrefix: map[string]*domain.ApiKey{}}
	for _, k := range keys {
		r.byID[k.ID()] = k
		r.byPrefix[k.Prefix()] = k
	}
	return r
}

func (r *fakeApiKeyRepo) Save(ctx context.Context, k *domain.ApiKey) error {
	r.byID[k.ID()] = k
	r.byPrefix[k.Prefix()] = k
	return nil
}
func (r *fakeApiKeyRepo) Update(ctx context.Context, k *domain.ApiKey) error {
	r.byID[k.ID()] = k
	return nil
}
func (r *fakeApiKeyRepo) FindByID(ctx context.Context, id domain.ApiKeyID) (*domain.ApiKey, error) {
	k, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrApiKeyNotFound
	}
	return k, nil
}
func (r *fakeApiKeyRepo) FindByPrefix(ctx context.Context, prefix string) (*domain.ApiKey, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	k, ok := r.byPrefix[prefix]
	if !ok {
		return nil, domain.ErrApiKeyNotFound
	}
	return k, nil
}

type fakeRefreshRepo struct {
	saved *application.RefreshToken
}

func (r *fakeRefreshRepo) Save(ctx context.Context, token application.RefreshToken) error {
	cp := token
	r.saved = &cp
	return nil
}
func (r *fakeRefreshRepo) FindByHash(ctx context.Context, h string) (*application.RefreshToken, error) {
	if r.saved != nil && r.saved.TokenHash == h {
		return r.saved, nil
	}
	return nil, domain.ErrUserNotFound
}
func (r *fakeRefreshRepo) Revoke(ctx context.Context, id, replacedBy string) error { return nil }

type fakeHasher struct {
	verifyResult bool
	verifyErr    error
}

func (h *fakeHasher) Hash(plaintext string) (string, error) { return "hash:" + plaintext, nil }
func (h *fakeHasher) Verify(plaintext, hash string) (bool, error) {
	return h.verifyResult, h.verifyErr
}

type fakeTokenIssuer struct {
	access        string
	refresh       string
	claimsByToken map[string]application.AccessTokenClaims
}

func (i *fakeTokenIssuer) IssueAccessToken(c application.AccessTokenClaims) (string, error) {
	if i.access == "" {
		return "access-token", nil
	}
	return i.access, nil
}
func (i *fakeTokenIssuer) IssueRefreshToken() (string, error) {
	if i.refresh == "" {
		return "refresh-token", nil
	}
	return i.refresh, nil
}
func (i *fakeTokenIssuer) VerifyAccessToken(tok string) (application.AccessTokenClaims, error) {
	if c, ok := i.claimsByToken[tok]; ok {
		return c, nil
	}
	return application.AccessTokenClaims{}, errors.New("invalid token")
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

// ── Builders de agregados ─────────────────────────────────────────────────────

func newActiveTenant(t *testing.T, env domain.Environment) *domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(domain.NewTenantID(), "Acme SA")
	if err != nil {
		t.Fatalf("build tenant: %v", err)
	}
	if err := tenant.Activate(env); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tenant.PullEvents()
	return tenant
}

func newPendingTenant(t *testing.T) *domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(domain.NewTenantID(), "Acme SA")
	if err != nil {
		t.Fatalf("build tenant: %v", err)
	}
	tenant.PullEvents()
	return tenant
}

func newActiveUser(t *testing.T, tenantID domain.TenantID, rawEmail string) *domain.User {
	t.Helper()
	email, err := domain.NewEmail(rawEmail)
	if err != nil {
		t.Fatalf("build email: %v", err)
	}
	u, err := domain.NewUser(domain.NewUserID(), tenantID, email, "hash:pw")
	if err != nil {
		t.Fatalf("build user: %v", err)
	}
	u.PullEvents()
	return u
}

func newApiKey(t *testing.T, tenantID domain.TenantID) *domain.ApiKey {
	t.Helper()
	k, err := domain.NewApiKey(
		domain.NewApiKeyID(), tenantID, "integration",
		"Xk3mPQrS", "hash:s3cr3t", domain.EnvironmentProduction,
		[]domain.Scope{domain.ScopePaymentsRead},
	)
	if err != nil {
		t.Fatalf("build api key: %v", err)
	}
	k.PullEvents()
	return k
}

// ── Router de test ────────────────────────────────────────────────────────────

// testEnv reúne el router real y los fakes que lo alimentan, para escribir
// tests HTTP end-to-end del contexto Auth.
type testEnv struct {
	tenants *fakeTenantRepo
	users   *fakeUserRepo
	members *fakeMembershipRepo
	apiKeys *fakeApiKeyRepo
	refresh *fakeRefreshRepo
	hasher  *fakeHasher
	issuer  *fakeTokenIssuer
	pub     *fakePublisher
	engine  *gin.Engine
}

func newTestEnv(
	tenants *fakeTenantRepo,
	users *fakeUserRepo,
	members *fakeMembershipRepo,
	apiKeys *fakeApiKeyRepo,
	hasher *fakeHasher,
	issuer *fakeTokenIssuer,
) *testEnv {
	refresh := &fakeRefreshRepo{}
	pub := &fakePublisher{}
	tx := fakeTx{}
	clock := realClockTest{}

	registerTenant := application.NewRegisterTenantUseCase(tenants, tx, pub)
	activateTenant := application.NewActivateTenantUseCase(tenants, tx, pub)
	suspendTenant := application.NewSuspendTenantUseCase(tenants, tx, pub)
	registerUser := application.NewRegisterUserUseCase(tenants, users, members, hasher, tx, pub)
	authenticate := application.NewAuthenticateUseCase(tenants, users, members, refresh, hasher, issuer, clock)
	refreshUC := application.NewRefreshTokenUseCase(users, members, tenants, refresh, hasher, issuer, clock)
	logoutUC := application.NewLogoutUseCase(refresh, hasher)
	issueApiKey := application.NewIssueApiKeyUseCase(tenants, apiKeys, hasher, tx, pub)
	revokeApiKey := application.NewRevokeApiKeyUseCase(apiKeys, tx, pub)
	assignRole := application.NewAssignRoleUseCase(tenants, users, members, tx, pub)

	engine := NewRouter(
		issuer, apiKeys, hasher,
		NewTenantHandler(registerTenant, activateTenant, suspendTenant),
		NewAuthHandler(authenticate, refreshUC, logoutUC),
		NewUserHandler(registerUser, assignRole),
		NewApiKeyHandler(issueApiKey, revokeApiKey),
	)

	return &testEnv{
		tenants: tenants, users: users, members: members, apiKeys: apiKeys,
		refresh: refresh, hasher: hasher, issuer: issuer, pub: pub, engine: engine,
	}
}

type realClockTest struct{}

func (realClockTest) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// do ejecuta un request contra el engine y retorna el recorder.
func (e *testEnv) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

// registerToken asocia un token opaco con unos claims para el fakeTokenIssuer.
func (e *testEnv) registerToken(token string, claims application.AccessTokenClaims) {
	if e.issuer.claimsByToken == nil {
		e.issuer.claimsByToken = map[string]application.AccessTokenClaims{}
	}
	e.issuer.claimsByToken[token] = claims
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// gin test context para tests unitarios de funciones que reciben *gin.Context.
func newTestCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}
