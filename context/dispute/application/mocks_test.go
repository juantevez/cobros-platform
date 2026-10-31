package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

var errBoom = errors.New("boom")

var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeTx struct{ err error }

func (t fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if t.err != nil {
		return t.err
	}
	return fn(ctx)
}

type fakeRepo struct {
	byID       map[domain.DisputeID]*domain.Dispute
	byPayment  map[string]*domain.Dispute
	listed     []*domain.Dispute
	overdue    []*domain.Dispute
	saved      []*domain.Dispute
	updated    []*domain.Dispute
	saveErr    error
	updateErr  error
	findErr    error
	findPayErr error
	listErr    error
	overdueErr error
	// overdueCalls cuenta las llamadas a ListOverdue (lotes del poller).
	overdueCalls int

	gotStatusFilter string
	gotLimit        int
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		byID:      map[domain.DisputeID]*domain.Dispute{},
		byPayment: map[string]*domain.Dispute{},
	}
}

func (r *fakeRepo) Save(ctx context.Context, d *domain.Dispute) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, d)
	r.byID[d.ID()] = d
	r.byPayment[d.PaymentID()] = d
	return nil
}

func (r *fakeRepo) Update(ctx context.Context, d *domain.Dispute) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, d)
	return nil
}

func (r *fakeRepo) FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDisputeNotFound
	}
	return d, nil
}

func (r *fakeRepo) FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error) {
	if r.findPayErr != nil {
		return nil, r.findPayErr
	}
	d, ok := r.byPayment[paymentID]
	if !ok {
		return nil, domain.ErrDisputeNotFound
	}
	return d, nil
}

func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Dispute, error) {
	r.gotStatusFilter = statusFilter
	r.gotLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listed, nil
}

func (r *fakeRepo) ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error) {
	r.overdueCalls++
	if r.overdueErr != nil {
		return nil, r.overdueErr
	}
	// Como la base: las vencidas que no se actualizaron con éxito, hasta limit.
	persisted := map[domain.DisputeID]bool{}
	for _, d := range r.updated {
		persisted[d.ID()] = true
	}
	var out []*domain.Dispute
	for _, d := range r.overdue {
		if !persisted[d.ID()] && len(out) < limit {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *fakeRepo) ListAll(ctx context.Context, statusFilter string, limit int) ([]*domain.Dispute, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listed, nil
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

// ── Fake PaymentReader ────────────────────────────────────────────────────────

// fakePayments implementa PaymentReader. Por defecto devuelve un pago capturado
// válido para cualquier payment_id; se puede forzar un ref particular o un error.
type fakePayments struct {
	refs map[string]domain.PaymentRef // opcional: refs específicos por ID
	err  error                        // opcional: error del reader
}

func (f fakePayments) FindPayment(ctx context.Context, paymentID string) (domain.PaymentRef, error) {
	if f.err != nil {
		return domain.PaymentRef{}, f.err
	}
	if ref, ok := f.refs[paymentID]; ok {
		return ref, nil
	}
	if paymentID == "" {
		return domain.PaymentRef{}, errors.New("payment not found")
	}
	return domain.PaymentRef{
		ID:       paymentID,
		TenantID: paymentTenant,
		Status:   "captured",
		Amount:   5000,
		Currency: "ARS",
	}, nil
}

// ── Helpers de agregados ──────────────────────────────────────────────────────

// paymentTenant es el dueño de los pagos que devuelve fakePayments por defecto.
var paymentTenant = uuid.NewString()

func capturedPayment(id string, tenantID domain.TenantID) domain.PaymentRef {
	return domain.PaymentRef{ID: id, TenantID: tenantID.String(), Status: "captured", Amount: 5000, Currency: "ARS"}
}

func seedOpen(r *fakeRepo, tenantID domain.TenantID, deadline time.Time) *domain.Dispute {
	d, _ := domain.NewDispute(domain.NewDisputeID(), capturedPayment("pay-1", tenantID), "psp-1", 5000, "ARS",
		domain.ReasonFraudulent, deadline, testNow)
	d.PullEvents()
	r.byID[d.ID()] = d
	r.byPayment[d.PaymentID()] = d
	return d
}
