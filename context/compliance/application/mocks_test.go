package application

import (
	"context"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

var errBoom = errors.New("boom")

var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeTx ejecuta la función inline (sin transacción real).
type fakeTx struct{ err error }

func (t fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if t.err != nil {
		return t.err
	}
	return fn(ctx)
}

type fakeAlertRepo struct {
	byID      map[domain.AlertID]*domain.Alert
	saved     []*domain.Alert
	updated   []*domain.Alert
	listed    []*domain.Alert
	saveErr   error
	updateErr error
	findErr   error
	listErr   error

	gotStatusFilter string
	gotTenantFilter string
	gotLimit        int
	gotFrom         domain.AlertStatus
}

func newAlertRepo() *fakeAlertRepo {
	return &fakeAlertRepo{byID: map[domain.AlertID]*domain.Alert{}}
}

func (r *fakeAlertRepo) Save(ctx context.Context, a *domain.Alert) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, a)
	r.byID[a.ID()] = a
	return nil
}

func (r *fakeAlertRepo) Update(ctx context.Context, a *domain.Alert, from domain.AlertStatus) error {
	r.gotFrom = from
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, a)
	return nil
}

func (r *fakeAlertRepo) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAlertNotFound
	}
	return a, nil
}

func (r *fakeAlertRepo) List(ctx context.Context, filter AlertFilter, limit int) ([]*domain.Alert, error) {
	r.gotStatusFilter = string(filter.Status)
	r.gotTenantFilter = filter.TenantID.String()
	r.gotLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listed, nil
}

type fakeWatchlist struct {
	matches   []domain.Match
	entries   []domain.WatchlistEntry
	screenErr error
	addErr    error
	listErr   error
	removeErr error

	added         []domain.WatchlistEntry
	removed       []string
	gotNormAdd    string
	gotNormScreen string
}

func (w *fakeWatchlist) Screen(ctx context.Context, normalizedName string) ([]domain.Match, error) {
	w.gotNormScreen = normalizedName
	if w.screenErr != nil {
		return nil, w.screenErr
	}
	return w.matches, nil
}

func (w *fakeWatchlist) Add(ctx context.Context, entry domain.WatchlistEntry, normalizedName string, addedAt time.Time) error {
	w.gotNormAdd = normalizedName
	if w.addErr != nil {
		return w.addErr
	}
	w.added = append(w.added, entry)
	return nil
}

func (w *fakeWatchlist) Remove(ctx context.Context, id string, removedAt time.Time) error {
	if w.removeErr != nil {
		return w.removeErr
	}
	w.removed = append(w.removed, id)
	return nil
}

func (w *fakeWatchlist) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	if w.listErr != nil {
		return nil, w.listErr
	}
	return w.entries, nil
}

type fakeTxLog struct {
	count     int
	sum       int64
	recordErr error
	countErr  error
	sumErr    error

	recorded    []MonitoredPayment
	gotSince    time.Time
	gotCurrency string
	sumCalls    int
}

func (l *fakeTxLog) Record(ctx context.Context, p MonitoredPayment) error {
	if l.recordErr != nil {
		return l.recordErr
	}
	l.recorded = append(l.recorded, p)
	return nil
}

func (l *fakeTxLog) CountSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	l.gotSince = since
	if l.countErr != nil {
		return 0, l.countErr
	}
	return l.count, nil
}

func (l *fakeTxLog) SumAmountSince(ctx context.Context, tenantID, currency string, since time.Time) (int64, error) {
	l.sumCalls++
	l.gotCurrency = currency
	if l.sumErr != nil {
		return 0, l.sumErr
	}
	return l.sum, nil
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

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newClock() fixedClock { return fixedClock{t: testNow} }

// ── Helpers ───────────────────────────────────────────────────────────────────

func matchOf(name, listType, country, source string, score int) domain.Match {
	return domain.Match{
		Entry: domain.WatchlistEntry{
			ID: "wl-1", FullName: name, ListType: listType, Country: country, Source: source,
		},
		Score: score,
	}
}
