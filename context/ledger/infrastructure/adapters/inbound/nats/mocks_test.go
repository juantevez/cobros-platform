package nats

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// ── Fake eventbus.Consumer ────────────────────────────────────────────────────

type fakeConsumer struct {
	gotCfg     eventbus.ConsumerConfig
	gotHandler eventbus.Handler
	startErr   error
}

func (c *fakeConsumer) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	c.gotCfg = cfg
	c.gotHandler = h
	return c.startErr
}

// ── Fakes de puertos de application ───────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeAccountRepo struct {
	byType    map[string]*domain.Account // key: accountType|currency
	saveCount int
	saveErr   error
}

func newFakeAccountRepo() *fakeAccountRepo {
	return &fakeAccountRepo{byType: map[string]*domain.Account{}}
}

func (r *fakeAccountRepo) seed(t *testing.T, tenantID domain.TenantID, at domain.AccountType, currency string) *domain.Account {
	t.Helper()
	acc, err := domain.NewAccount(domain.NewAccountID(), tenantID, at, currency, "")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	acc.PullEvents()
	r.byType[at.String()+"|"+currency] = acc
	return acc
}

func (r *fakeAccountRepo) Save(ctx context.Context, a *domain.Account) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saveCount++
	r.byType[a.AccountType().String()+"|"+a.Currency()] = a
	return nil
}
func (r *fakeAccountRepo) FindByID(ctx context.Context, id domain.AccountID) (*domain.Account, error) {
	return nil, domain.ErrAccountNotFound
}
func (r *fakeAccountRepo) FindByTenantAndType(ctx context.Context, tid domain.TenantID, at domain.AccountType, cur string) (*domain.Account, error) {
	a, ok := r.byType[at.String()+"|"+cur]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return a, nil
}

type fakeEntryRepo struct {
	saved   []*domain.JournalEntry
	saveErr error
}

func newFakeEntryRepo() *fakeEntryRepo { return &fakeEntryRepo{} }

func (r *fakeEntryRepo) Save(ctx context.Context, e *domain.JournalEntry) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, e)
	return nil
}
func (r *fakeEntryRepo) FindByID(ctx context.Context, id domain.EntryID) (*domain.JournalEntry, error) {
	return nil, domain.ErrEntryNotFound
}
func (r *fakeEntryRepo) FindByIdempotencyKey(ctx context.Context, tid domain.TenantID, key string) (*domain.JournalEntry, error) {
	return nil, domain.ErrEntryNotFound // nunca existe → siempre crea uno nuevo
}

type fakeBalanceRepo struct{}

func (fakeBalanceRepo) Apply(ctx context.Context, postings []domain.Posting) error { return nil }
func (fakeBalanceRepo) GetBalance(ctx context.Context, id domain.AccountID) (int64, error) {
	return 0, nil
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// ── Helpers ───────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func validUUID() string { return uuid.NewString() }

func testTenantID(t *testing.T) domain.TenantID {
	t.Helper()
	id, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return id
}

// newPostEntry arma un PostEntryUseCase real con fakes y devuelve el entryRepo
// y el publisher para inspeccionar el resultado.
func newPostEntry() (*application.PostEntryUseCase, *fakeEntryRepo, *fakePublisher) {
	entryRepo := newFakeEntryRepo()
	pub := &fakePublisher{}
	uc := application.NewPostEntryUseCase(entryRepo, fakeBalanceRepo{}, fakeTx{}, pub, fakeClock{})
	return uc, entryRepo, pub
}

// newCreateAccount arma un CreateAccountUseCase real con el accountRepo dado.
func newCreateAccount(accountRepo *fakeAccountRepo) (*application.CreateAccountUseCase, *fakePublisher) {
	pub := &fakePublisher{}
	uc := application.NewCreateAccountUseCase(accountRepo, fakeTx{}, pub)
	return uc, pub
}
