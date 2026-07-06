package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ── Fakes de puertos del contexto Ledger ──────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeAccountRepo struct {
	byID    map[domain.AccountID]*domain.Account
	saveErr error
}

func newFakeAccountRepo(accs ...*domain.Account) *fakeAccountRepo {
	m := map[domain.AccountID]*domain.Account{}
	for _, a := range accs {
		m[a.ID()] = a
	}
	return &fakeAccountRepo{byID: m}
}

func (r *fakeAccountRepo) Save(ctx context.Context, a *domain.Account) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[a.ID()] = a
	return nil
}
func (r *fakeAccountRepo) FindByID(ctx context.Context, id domain.AccountID) (*domain.Account, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return a, nil
}
func (r *fakeAccountRepo) FindByTenantAndType(ctx context.Context, tid domain.TenantID, at domain.AccountType, cur string) (*domain.Account, error) {
	return nil, domain.ErrAccountNotFound
}

type fakeEntryRepo struct {
	byID          map[domain.EntryID]*domain.JournalEntry
	byIdempotency map[string]*domain.JournalEntry
}

func newFakeEntryRepo() *fakeEntryRepo {
	return &fakeEntryRepo{
		byID:          map[domain.EntryID]*domain.JournalEntry{},
		byIdempotency: map[string]*domain.JournalEntry{},
	}
}

func (r *fakeEntryRepo) preload(e *domain.JournalEntry) {
	r.byID[e.ID()] = e
	r.byIdempotency[e.IdempotencyKey()] = e
}
func (r *fakeEntryRepo) Save(ctx context.Context, e *domain.JournalEntry) error {
	r.byID[e.ID()] = e
	r.byIdempotency[e.IdempotencyKey()] = e
	return nil
}
func (r *fakeEntryRepo) FindByID(ctx context.Context, id domain.EntryID) (*domain.JournalEntry, error) {
	e, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrEntryNotFound
	}
	return e, nil
}
func (r *fakeEntryRepo) FindByIdempotencyKey(ctx context.Context, tid domain.TenantID, key string) (*domain.JournalEntry, error) {
	e, ok := r.byIdempotency[key]
	if !ok {
		return nil, domain.ErrEntryNotFound
	}
	return e, nil
}

type fakeBalanceRepo struct {
	balance int64
}

func (r *fakeBalanceRepo) Apply(ctx context.Context, postings []domain.Posting) error { return nil }
func (r *fakeBalanceRepo) GetBalance(ctx context.Context, id domain.AccountID) (int64, error) {
	return r.balance, nil
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// ── testEnv ───────────────────────────────────────────────────────────────────

type testEnv struct {
	accounts *fakeAccountRepo
	entries  *fakeEntryRepo
	balances *fakeBalanceRepo
	pub      *fakePublisher
	engine   *gin.Engine
	tenantID domain.TenantID
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	accounts := newFakeAccountRepo()
	entries := newFakeEntryRepo()
	balances := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	tx := fakeTx{}
	clock := fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}

	createAccount := application.NewCreateAccountUseCase(accounts, tx, pub)
	getBalance := application.NewGetBalanceUseCase(accounts, balances)
	postEntry := application.NewPostEntryUseCase(entries, balances, tx, pub, clock)
	reverseEntry := application.NewReverseEntryUseCase(entries, balances, tx, pub)

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
	RegisterRoutes(grp, NewAccountHandler(createAccount, getBalance), NewEntryHandler(postEntry, reverseEntry))

	return &testEnv{
		accounts: accounts, entries: entries, balances: balances,
		pub: pub, engine: r, tenantID: tenantID,
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

// buildEntry construye un asiento balanceado del tenant dado.
func buildEntry(t *testing.T, tenantID domain.TenantID, key string) *domain.JournalEntry {
	t.Helper()
	e, err := domain.NewJournalEntry(
		domain.NewEntryID(), tenantID, key, "asiento", nil, time.Now().UTC(),
		[]domain.PostingInput{
			{AccountID: domain.NewAccountID(), Direction: domain.DirectionDebit, Amount: 100, Currency: "ARS"},
			{AccountID: domain.NewAccountID(), Direction: domain.DirectionCredit, Amount: 100, Currency: "ARS"},
		},
	)
	if err != nil {
		t.Fatalf("build entry: %v", err)
	}
	e.PullEvents()
	return e
}

// balancedLines retorna 2 líneas de request balanceadas.
func balancedLines() []map[string]any {
	return []map[string]any{
		{"account_id": uuid.NewString(), "direction": "debit", "amount": 100, "currency": "ARS"},
		{"account_id": uuid.NewString(), "direction": "credit", "amount": 100, "currency": "ARS"},
	}
}
