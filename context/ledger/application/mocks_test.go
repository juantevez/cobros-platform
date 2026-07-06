package application

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// mocks_test.go: test doubles in-memory de los puertos del contexto Ledger.

// ── TxManager ─────────────────────────────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// ── AccountRepository ─────────────────────────────────────────────────────────

type fakeAccountRepo struct {
	byID    map[domain.AccountID]*domain.Account
	saved   *domain.Account
	saveErr error
	findErr error
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
	r.saved = a
	r.byID[a.ID()] = a
	return nil
}
func (r *fakeAccountRepo) FindByID(ctx context.Context, id domain.AccountID) (*domain.Account, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return a, nil
}
func (r *fakeAccountRepo) FindByTenantAndType(ctx context.Context, tid domain.TenantID, at domain.AccountType, cur string) (*domain.Account, error) {
	return nil, domain.ErrAccountNotFound
}

// ── EntryRepository ───────────────────────────────────────────────────────────

type fakeEntryRepo struct {
	byID          map[domain.EntryID]*domain.JournalEntry
	byIdempotency map[string]*domain.JournalEntry
	saved         []*domain.JournalEntry
	saveErr       error
	idempotencyErr error // error no-NotFound al chequear idempotencia
	findByIDErr   error
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
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, e)
	r.byID[e.ID()] = e
	r.byIdempotency[e.IdempotencyKey()] = e
	return nil
}
func (r *fakeEntryRepo) FindByID(ctx context.Context, id domain.EntryID) (*domain.JournalEntry, error) {
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	e, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrEntryNotFound
	}
	return e, nil
}
func (r *fakeEntryRepo) FindByIdempotencyKey(ctx context.Context, tid domain.TenantID, key string) (*domain.JournalEntry, error) {
	if r.idempotencyErr != nil {
		return nil, r.idempotencyErr
	}
	e, ok := r.byIdempotency[key]
	if !ok {
		return nil, domain.ErrEntryNotFound
	}
	return e, nil
}

// ── BalanceRepository ─────────────────────────────────────────────────────────

type fakeBalanceRepo struct {
	applied    [][]domain.Posting
	applyErr   error
	balance    int64
	balanceErr error
}

func (r *fakeBalanceRepo) Apply(ctx context.Context, postings []domain.Posting) error {
	if r.applyErr != nil {
		return r.applyErr
	}
	r.applied = append(r.applied, postings)
	return nil
}
func (r *fakeBalanceRepo) GetBalance(ctx context.Context, id domain.AccountID) (int64, error) {
	if r.balanceErr != nil {
		return 0, r.balanceErr
	}
	return r.balance, nil
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

// ── Helpers ───────────────────────────────────────────────────────────────────

func validUUID() string { return uuid.NewString() }

func testTenantID(t *testing.T) domain.TenantID {
	t.Helper()
	id, err := domain.ParseTenantID(validUUID())
	if err != nil {
		t.Fatalf("build tenant id: %v", err)
	}
	return id
}

func balancedLines() []PostingLine {
	return []PostingLine{
		{AccountID: validUUID(), Direction: "debit", Amount: 100, Currency: "ARS"},
		{AccountID: validUUID(), Direction: "credit", Amount: 100, Currency: "ARS"},
	}
}

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
