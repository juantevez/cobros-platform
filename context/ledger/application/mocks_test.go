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
	// raceAccount simula una carrera: Save la "inserta" (como si otra ejecución
	// la hubiera creado) y falla con ErrAccountAlreadyExists.
	raceAccount *domain.Account
}

func newFakeAccountRepo(accs ...*domain.Account) *fakeAccountRepo {
	m := map[domain.AccountID]*domain.Account{}
	for _, a := range accs {
		m[a.ID()] = a
	}
	return &fakeAccountRepo{byID: m}
}

func (r *fakeAccountRepo) Save(ctx context.Context, a *domain.Account) error {
	if r.raceAccount != nil {
		r.byID[r.raceAccount.ID()] = r.raceAccount
		return domain.ErrAccountAlreadyExists
	}
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
	if r.findErr != nil {
		return nil, r.findErr
	}
	for _, a := range r.byID {
		if a.TenantID() == tid && a.AccountType() == at && a.Currency() == cur {
			return a, nil
		}
	}
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

func newTestAccount(t *testing.T, tenantID domain.TenantID, at domain.AccountType, currency string) *domain.Account {
	t.Helper()
	acc, err := domain.NewAccount(domain.NewAccountID(), tenantID, at, currency, "")
	if err != nil {
		t.Fatalf("build account: %v", err)
	}
	acc.PullEvents()
	return acc
}

// entryFixture es un tenant con dos cuentas ARS existentes, para postear
// asientos que pasen la validación de cuentas de PostEntry.
type entryFixture struct {
	tenantID domain.TenantID
	accounts *fakeAccountRepo
	debit    *domain.Account
	credit   *domain.Account
}

func newEntryFixture(t *testing.T) *entryFixture {
	t.Helper()
	tenantID := testTenantID(t)
	debit := newTestAccount(t, tenantID, domain.AccountTypeInTransit, "ARS")
	credit := newTestAccount(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	return &entryFixture{
		tenantID: tenantID,
		accounts: newFakeAccountRepo(debit, credit),
		debit:    debit,
		credit:   credit,
	}
}

// lines retorna un débito y un crédito ARS sobre las cuentas del fixture.
func (f *entryFixture) lines(debitAmount, creditAmount int64) []PostingLine {
	return []PostingLine{
		{AccountID: f.debit.ID().String(), Direction: "debit", Amount: debitAmount, Currency: "ARS"},
		{AccountID: f.credit.ID().String(), Direction: "credit", Amount: creditAmount, Currency: "ARS"},
	}
}

func (f *entryFixture) balancedLines() []PostingLine { return f.lines(100, 100) }

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
