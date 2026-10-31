package application

import (
	"context"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

var errBoom = errors.New("boom")

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeRepo implementa PayoutRepository en memoria. Registra el estado del
// payout en cada Update para poder verificar la secuencia de transiciones.
type fakeRepo struct {
	saved       []*domain.Payout
	updates     []domain.PayoutStatus
	byID        map[domain.PayoutID]*domain.Payout
	byKey       map[string]*domain.Payout // payouts indexados por idempotency key
	list        []*domain.Payout
	saveErr     error
	updateErr   error
	updateErrAt int // si > 0, sólo falla el Update número N (1-based)
	findErr     error
	findKeyErr  error // error inyectado en FindByIdempotencyKey (distinto de NotFound)
	listErr     error
	// froms registra el estado de partida de cada Update; changed hace que
	// Update devuelva ErrPayoutChanged (otra ejecución lo resolvió antes).
	froms   []domain.PayoutStatus
	changed bool
	// unresolved es lo que devuelve ListUnresolved; unresolvedBefore, el corte.
	unresolved       []*domain.Payout
	unresolvedErr    error
	unresolvedBefore time.Time

	// capturados para aserciones
	lastTenantID domain.TenantID
	lastLimit    int
}

func (r *fakeRepo) Save(ctx context.Context, p *domain.Payout) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, p)
	// Simula el índice UNIQUE (tenant, idempotency_key): los replays de retry
	// encuentran el payout por FindByIdempotencyKey igual que en Postgres.
	if r.byID == nil {
		r.byID = map[domain.PayoutID]*domain.Payout{}
	}
	if r.byKey == nil {
		r.byKey = map[string]*domain.Payout{}
	}
	r.byID[p.ID()] = p
	r.byKey[p.IdempotencyKey()] = p
	return nil
}

func (r *fakeRepo) Update(ctx context.Context, p *domain.Payout, from domain.PayoutStatus) error {
	r.updates = append(r.updates, p.Status())
	r.froms = append(r.froms, from)
	if r.updateErr != nil && (r.updateErrAt == 0 || r.updateErrAt == len(r.updates)) {
		return r.updateErr
	}
	if r.changed {
		return domain.ErrPayoutChanged
	}
	// El update persiste la transición sobre la copia canónica del repo, de
	// modo que un replay posterior observe el estado final (confirmed/failed).
	if r.byID != nil {
		r.byID[p.ID()] = p
	}
	return nil
}

func (r *fakeRepo) ListUnresolved(ctx context.Context, olderThan time.Time, limit int) ([]*domain.Payout, error) {
	r.unresolvedBefore = olderThan
	return r.unresolved, r.unresolvedErr
}

func (r *fakeRepo) FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPayoutNotFound
	}
	return p, nil
}

func (r *fakeRepo) FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payout, error) {
	if r.findKeyErr != nil {
		return nil, r.findKeyErr
	}
	p, ok := r.byKey[key]
	if !ok {
		return nil, domain.ErrPayoutNotFound
	}
	return p, nil
}

func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Payout, error) {
	r.lastTenantID = tenantID
	r.lastLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.list, nil
}

// fakeBalanceChecker devuelve un saldo fijo o un error.
type fakeBalanceChecker struct {
	balance      int64
	err          error
	calls        int
	lastCurrency string
}

func (b *fakeBalanceChecker) GetAvailableBalance(ctx context.Context, tenantID domain.TenantID, currency string) (int64, error) {
	b.calls++
	b.lastCurrency = currency
	if b.err != nil {
		return 0, b.err
	}
	return b.balance, nil
}

// fakeBankProvider devuelve una cuenta bancaria fija o un error.
type fakeBankProvider struct {
	account domain.BankAccountInfo
	err     error
}

func (b *fakeBankProvider) GetBankAccount(ctx context.Context, tenantID domain.TenantID) (domain.BankAccountInfo, error) {
	if b.err != nil {
		return domain.BankAccountInfo{}, b.err
	}
	return b.account, nil
}

// fakeTransfer captura la request y devuelve un resultado o error configurado.
type fakeTransfer struct {
	result    TransferResult
	err       error
	reqs      []TransferRequest
	lookup    TransferLookup
	lookupErr error
	lookups   []TransferLookupRequest
}

func (t *fakeTransfer) GetTransfer(ctx context.Context, req TransferLookupRequest) (TransferLookup, error) {
	t.lookups = append(t.lookups, req)
	return t.lookup, t.lookupErr
}

func (t *fakeTransfer) Transfer(ctx context.Context, req TransferRequest) (TransferResult, error) {
	t.reqs = append(t.reqs, req)
	if t.err != nil {
		return TransferResult{}, t.err
	}
	return t.result, nil
}

func (t *fakeTransfer) Name() string { return "fake" }

// fakeTx ejecuta la función directamente. Si err != nil, falla sin ejecutarla.
type fakeTx struct {
	calls int
	err   error
}

func (tx *fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	tx.calls++
	if tx.err != nil {
		return tx.err
	}
	return fn(ctx)
}

// fakePublisher acumula eventos publicados. errAt > 0 hace fallar sólo la
// llamada número N (1-based); con errAt == 0, err aplica a todas.
type fakePublisher struct {
	events []domain.Event
	calls  int
	err    error
	errAt  int
}

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.calls++
	if p.err != nil && (p.errAt == 0 || p.errAt == p.calls) {
		return p.err
	}
	p.events = append(p.events, events...)
	return nil
}

func (p *fakePublisher) eventTypes() []string {
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.EventType()
	}
	return out
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func validBankAccount() domain.BankAccountInfo {
	ba, _ := domain.NewBankAccountInfo("CBU", "0170099220000067797370", "Banco Test", "Comercio SA", "ARS")
	return ba
}

// buildPayout crea un payout real en estado initiated (sin eventos pendientes).
func buildPayout(tenantID string, amount int64) *domain.Payout {
	money, _ := domain.NewMoney(amount, "ARS")
	p, _ := domain.NewPayout(domain.NewPayoutID(), domain.TenantID(tenantID), money, validBankAccount(), "build_"+tenantID)
	p.PullEvents()
	return p
}

// buildConfirmedPayout reconstituye un payout confirmado con timestamps fijos.
func buildConfirmedPayout(tenantID string, ref string, at time.Time) *domain.Payout {
	return domain.ReconstitutePayout(
		domain.NewPayoutID(), domain.TenantID(tenantID),
		domain.ReconstituteMoney(5000, "ARS"), validBankAccount(),
		domain.StatusConfirmed,
		ref, "", "payout_initiated_x",
		&at, &at, nil,
		at, at,
	)
}
