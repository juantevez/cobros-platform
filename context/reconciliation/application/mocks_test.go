package application

import (
	"context"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

var errBoom = errors.New("boom")

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

var (
	periodFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	periodTo   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeRunRepo implementa RunRepository en memoria. Registra el estado del run
// en cada Update para verificar la secuencia de transiciones.
type fakeRunRepo struct {
	byID        map[domain.RunID]*domain.ReconciliationRun
	saved       []*domain.ReconciliationRun
	updates     []domain.RunStatus
	froms       []domain.RunStatus // estado esperado en cada Update
	list        []*domain.ReconciliationRun
	saveErr     error
	updateErr   error
	updateErrAt int // si > 0, sólo falla el Update número N (1-based)
	findErr     error
	listErr     error

	// capturados para aserciones
	listCalled bool
	lastTenant domain.TenantID
	lastLimit  int
}

func newRunRepo(seed ...*domain.ReconciliationRun) *fakeRunRepo {
	r := &fakeRunRepo{byID: map[domain.RunID]*domain.ReconciliationRun{}}
	for _, run := range seed {
		r.byID[run.ID()] = run
	}
	return r
}

func (r *fakeRunRepo) Save(ctx context.Context, run *domain.ReconciliationRun) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, run)
	return nil
}

func (r *fakeRunRepo) Update(ctx context.Context, run *domain.ReconciliationRun, from domain.RunStatus) error {
	r.updates = append(r.updates, run.Status())
	r.froms = append(r.froms, from)
	if r.updateErr != nil && (r.updateErrAt == 0 || r.updateErrAt == len(r.updates)) {
		return r.updateErr
	}
	return nil
}

func (r *fakeRunRepo) FindByID(ctx context.Context, id domain.RunID) (*domain.ReconciliationRun, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	run, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrRunNotFound
	}
	return run, nil
}

func (r *fakeRunRepo) List(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.ReconciliationRun, error) {
	r.listCalled = true
	r.lastTenant = tenantID
	r.lastLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.list, nil
}

// fakeDiscRepo implementa DiscrepancyRepository en memoria.
type fakeDiscRepo struct {
	byID      map[domain.DiscrepancyID]*domain.Discrepancy
	saved     []*domain.Discrepancy
	saveCalls int
	updated   []*domain.Discrepancy
	list      []*domain.Discrepancy
	saveErr   error
	updateErr error
	findErr   error
	listErr   error

	// capturados para aserciones
	lastRunID  domain.RunID
	lastFilter string
	lastLimit  int
}

func newDiscRepo(seed ...*domain.Discrepancy) *fakeDiscRepo {
	r := &fakeDiscRepo{byID: map[domain.DiscrepancyID]*domain.Discrepancy{}}
	for _, d := range seed {
		r.byID[d.ID()] = d
	}
	return r
}

func (r *fakeDiscRepo) SaveAll(ctx context.Context, ds []*domain.Discrepancy) error {
	r.saveCalls++
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, ds...)
	return nil
}

func (r *fakeDiscRepo) Update(ctx context.Context, d *domain.Discrepancy) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, d)
	return nil
}

func (r *fakeDiscRepo) FindByID(ctx context.Context, id domain.DiscrepancyID) (*domain.Discrepancy, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDiscrepancyNotFound
	}
	return d, nil
}

func (r *fakeDiscRepo) ListByRun(ctx context.Context, runID domain.RunID, statusFilter string, limit int) ([]*domain.Discrepancy, error) {
	r.lastRunID = runID
	r.lastFilter = statusFilter
	r.lastLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.list, nil
}

// byRecord indexa las discrepancias guardadas por RecordID (el matching
// itera mapas, así que el orden no es determinista).
func (r *fakeDiscRepo) byRecord() map[string]*domain.Discrepancy {
	out := make(map[string]*domain.Discrepancy, len(r.saved))
	for _, d := range r.saved {
		out[d.RecordID()] = d
	}
	return out
}

// fakePaymentReader devuelve pagos fijos o un error.
type fakePaymentReader struct {
	payments   []SystemPayment
	err        error
	calls      int
	lastTenant domain.TenantID
	lastFrom   time.Time
	lastTo     time.Time
}

func (f *fakePaymentReader) ReadByPeriod(ctx context.Context, tenantID domain.TenantID, from, to time.Time) ([]SystemPayment, error) {
	f.calls++
	f.lastTenant, f.lastFrom, f.lastTo = tenantID, from, to
	if f.err != nil {
		return nil, f.err
	}
	return f.payments, nil
}

// fakeParser devuelve registros fijos o un error.
type fakeParser struct {
	records  []PSPRecord
	err      error
	lastData []byte
}

func (f *fakeParser) Parse(data []byte) ([]PSPRecord, error) {
	f.lastData = data
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

// fakeLedger devuelve un imbalance fijo o un error.
type fakeLedger struct {
	imbalance  int64
	err        error
	lastTenant domain.TenantID
	lastFrom   time.Time
	lastTo     time.Time
}

func (f *fakeLedger) CheckBalance(ctx context.Context, tenantID domain.TenantID, from, to time.Time) (int64, error) {
	f.lastTenant, f.lastFrom, f.lastTo = tenantID, from, to
	if f.err != nil {
		return 0, f.err
	}
	return f.imbalance, nil
}

// fakePublisher acumula eventos publicados o falla con err.
type fakePublisher struct {
	events []domain.Event
	calls  int
	err    error
}

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.calls++
	if p.err != nil {
		return p.err
	}
	p.events = append(p.events, events...)
	return nil
}

// fakeTx ejecuta fn directamente, o falla antes con err. Cuenta las
// transacciones y si fn falló dentro de alguna (rollback).
type fakeTx struct {
	calls     int
	rollbacks int
	err       error
}

func (f *fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if err := fn(ctx); err != nil {
		f.rollbacks++
		return err
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// pendingRun crea un run real en estado pending para el tenant dado.
func pendingRun(tenantID string, t domain.ReconciliationType) *domain.ReconciliationRun {
	run, _ := domain.NewReconciliationRun(domain.NewRunID(), domain.TenantID(tenantID), t, periodFrom, periodTo)
	return run
}

func runWithStatus(st domain.RunStatus) *domain.ReconciliationRun {
	return domain.ReconstituteRun(
		domain.NewRunID(), domain.TenantID(tenantA), domain.TypePayment, st,
		periodFrom, periodTo, 0, 0, 0, "", periodTo, nil,
	)
}

func openDiscrepancy() *domain.Discrepancy {
	return domain.NewDiscrepancy(
		domain.NewDiscrepancyID(), domain.NewRunID(), domain.TenantID(tenantA),
		domain.DiscrepancyAmountMismatch, "PSP-1", "100 ARS", "90 ARS",
	)
}
