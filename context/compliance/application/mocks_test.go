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
	gotLimit        int
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

func (r *fakeAlertRepo) Update(ctx context.Context, a *domain.Alert) error {
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

func (r *fakeAlertRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Alert, error) {
	r.gotStatusFilter = statusFilter
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

	added         []domain.WatchlistEntry
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

func (w *fakeWatchlist) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	if w.listErr != nil {
		return nil, w.listErr
	}
	return w.entries, nil
}

type fakeTxReader struct {
	count int
	err   error

	gotSince time.Time
}

func (r *fakeTxReader) CountCapturedSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	r.gotSince = since
	if r.err != nil {
		return 0, r.err
	}
	return r.count, nil
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
