package application

import (
	"context"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// mocks_test.go define test doubles in-memory para los puertos del contexto Auth.
// Son deterministas y programables por campo, sin dependencias externas.

// ── TxManager ─────────────────────────────────────────────────────────────────

// fakeTx ejecuta la función directamente (sin transacción real).
type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// ── TenantRepository ──────────────────────────────────────────────────────────

type fakeTenantRepo struct {
	tenants   map[domain.TenantID]*domain.Tenant
	findErr   error
	updateErr error
	saveErr   error
	updated   *domain.Tenant
}

func newFakeTenantRepo(tenants ...*domain.Tenant) *fakeTenantRepo {
	m := map[domain.TenantID]*domain.Tenant{}
	for _, t := range tenants {
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
	r.updated = t
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

// ── UserRepository ────────────────────────────────────────────────────────────

type fakeUserRepo struct {
	byID    map[domain.UserID]*domain.User
	byEmail map[string]*domain.User // key: tenantID|email
	findErr error
	saveErr error
}

func newFakeUserRepo(users ...*domain.User) *fakeUserRepo {
	r := &fakeUserRepo{byID: map[domain.UserID]*domain.User{}, byEmail: map[string]*domain.User{}}
	for _, u := range users {
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
	if r.findErr != nil {
		return nil, r.findErr
	}
	u, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) FindByEmail(ctx context.Context, tenantID domain.TenantID, email domain.Email) (*domain.User, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	u, ok := r.byEmail[tenantID.String()+"|"+email.String()]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return u, nil
}

// ── MembershipRepository ──────────────────────────────────────────────────────

type fakeMembershipRepo struct {
	items    map[string]domain.Membership // key: userID|tenantID
	saved    *domain.Membership
	updated  *domain.Membership
	findErr  error
}

func newFakeMembershipRepo(memberships ...domain.Membership) *fakeMembershipRepo {
	r := &fakeMembershipRepo{items: map[string]domain.Membership{}}
	for _, m := range memberships {
		r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	}
	return r
}

func (r *fakeMembershipRepo) Save(ctx context.Context, m domain.Membership) error {
	cp := m
	r.saved = &cp
	r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	return nil
}

func (r *fakeMembershipRepo) Update(ctx context.Context, m domain.Membership) error {
	cp := m
	r.updated = &cp
	r.items[m.UserID().String()+"|"+m.TenantID().String()] = m
	return nil
}

func (r *fakeMembershipRepo) FindByUserAndTenant(ctx context.Context, userID domain.UserID, tenantID domain.TenantID) (*domain.Membership, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	m, ok := r.items[userID.String()+"|"+tenantID.String()]
	if !ok {
		return nil, domain.ErrMembershipNotFound
	}
	return &m, nil
}

func (r *fakeMembershipRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]domain.Membership, error) {
	var out []domain.Membership
	for _, m := range r.items {
		if m.TenantID() == tenantID {
			out = append(out, m)
		}
	}
	return out, nil
}

// ── ApiKeyRepository ──────────────────────────────────────────────────────────

type fakeApiKeyRepo struct {
	saved   *domain.ApiKey
	saveErr error
	byID    map[domain.ApiKeyID]*domain.ApiKey
}

func newFakeApiKeyRepo() *fakeApiKeyRepo {
	return &fakeApiKeyRepo{byID: map[domain.ApiKeyID]*domain.ApiKey{}}
}

func (r *fakeApiKeyRepo) Save(ctx context.Context, k *domain.ApiKey) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = k
	r.byID[k.ID()] = k
	return nil
}

func (r *fakeApiKeyRepo) Update(ctx context.Context, k *domain.ApiKey) error { r.byID[k.ID()] = k; return nil }

func (r *fakeApiKeyRepo) FindByID(ctx context.Context, id domain.ApiKeyID) (*domain.ApiKey, error) {
	k, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrApiKeyNotFound
	}
	return k, nil
}

func (r *fakeApiKeyRepo) FindByPrefix(ctx context.Context, prefix string) (*domain.ApiKey, error) {
	for _, k := range r.byID {
		if k.Prefix() == prefix {
			return k, nil
		}
	}
	return nil, domain.ErrApiKeyNotFound
}

// ── RefreshTokenRepository ────────────────────────────────────────────────────

type fakeRefreshRepo struct {
	byHash        map[string]*RefreshToken // tokens precargados, por hash
	saved         *RefreshToken
	saveErr       error
	revokedID     string
	revokedByID   string
	revokeCalled  bool
	revokeErr     error
}

func (r *fakeRefreshRepo) Save(ctx context.Context, token RefreshToken) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	cp := token
	r.saved = &cp
	return nil
}

func (r *fakeRefreshRepo) FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	if r.byHash != nil {
		if tok, ok := r.byHash[tokenHash]; ok {
			return tok, nil
		}
	}
	if r.saved != nil && r.saved.TokenHash == tokenHash {
		return r.saved, nil
	}
	return nil, domain.ErrUserNotFound
}

func (r *fakeRefreshRepo) Revoke(ctx context.Context, tokenID, replacedBy string) error {
	if r.revokeErr != nil {
		return r.revokeErr
	}
	r.revokeCalled = true
	r.revokedID = tokenID
	r.revokedByID = replacedBy
	return nil
}

// ── PasswordHasher ────────────────────────────────────────────────────────────

type fakeHasher struct {
	verifyResult bool
	verifyErr    error
	hashErr      error
}

func (h *fakeHasher) Hash(plaintext string) (string, error) {
	if h.hashErr != nil {
		return "", h.hashErr
	}
	return "hash:" + plaintext, nil
}

func (h *fakeHasher) Verify(plaintext, hash string) (bool, error) {
	if h.verifyErr != nil {
		return false, h.verifyErr
	}
	return h.verifyResult, nil
}

// ── TokenIssuer ───────────────────────────────────────────────────────────────

type fakeTokenIssuer struct {
	access     string
	refresh    string
	accessErr  error
	refreshErr error
	gotClaims  AccessTokenClaims
}

func (i *fakeTokenIssuer) IssueAccessToken(claims AccessTokenClaims) (string, error) {
	i.gotClaims = claims
	if i.accessErr != nil {
		return "", i.accessErr
	}
	return i.access, nil
}

func (i *fakeTokenIssuer) IssueRefreshToken() (string, error) {
	if i.refreshErr != nil {
		return "", i.refreshErr
	}
	return i.refresh, nil
}

func (i *fakeTokenIssuer) VerifyAccessToken(tokenStr string) (AccessTokenClaims, error) {
	return AccessTokenClaims{}, nil
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

// ── Clock ─────────────────────────────────────────────────────────────────────

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// ── Helpers de construcción de agregados ──────────────────────────────────────

func newActiveTenant(t *testing.T, env domain.Environment) *domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(domain.NewTenantID(), "Acme SA")
	if err != nil {
		t.Fatalf("build tenant: %v", err)
	}
	if err := tenant.Activate(env); err != nil {
		t.Fatalf("activate tenant: %v", err)
	}
	tenant.PullEvents() // limpiar eventos de setup
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
		"Xk3mPQrS", "keyhash", domain.EnvironmentProduction,
		[]domain.Scope{domain.ScopePaymentsRead},
	)
	if err != nil {
		t.Fatalf("build api key: %v", err)
	}
	k.PullEvents()
	return k
}
