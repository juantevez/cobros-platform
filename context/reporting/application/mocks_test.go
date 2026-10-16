package application

import (
	"context"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

var errBoom = errors.New("boom")

const tenantA = "11111111-1111-1111-1111-111111111111"

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeWriter implementa ProjectionWriter en memoria. errOnMovement > 0 hace
// fallar sólo el SaveLedgerMovement número N (1-based).
type fakeWriter struct {
	payments      []domain.PaymentFact
	movements     []domain.LedgerMovement
	paymentErr    error
	movementErr   error
	errOnMovement int
	movementCalls int
}

func (w *fakeWriter) SavePaymentFact(ctx context.Context, f domain.PaymentFact) error {
	if w.paymentErr != nil {
		return w.paymentErr
	}
	w.payments = append(w.payments, f)
	return nil
}

func (w *fakeWriter) SaveLedgerMovement(ctx context.Context, m domain.LedgerMovement) error {
	w.movementCalls++
	if w.movementErr != nil && (w.errOnMovement == 0 || w.errOnMovement == w.movementCalls) {
		return w.movementErr
	}
	w.movements = append(w.movements, m)
	return nil
}

// fakeAccounts resuelve tipos de cuenta desde un mapa; cuentas desconocidas
// devuelven tipo vacío sin error (igual que el adapter real).
type fakeAccounts struct {
	types   map[string]string
	err     error
	lookups []string
}

func (a *fakeAccounts) AccountType(ctx context.Context, accountID string) (string, error) {
	a.lookups = append(a.lookups, accountID)
	if a.err != nil {
		return "", a.err
	}
	return a.types[accountID], nil
}

// fixedClock devuelve siempre el mismo instante.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// fakeReader implementa ReportReader capturando las queries recibidas.
type fakeReader struct {
	volume   []domain.VolumePoint
	revenue  []domain.RevenueSummary
	balances []domain.TenantBalance
	err      error

	volumeCalls, revenueCalls, balanceCalls int
	lastVolume                              VolumeQuery
	lastRevenue                             RevenueQuery
	lastTenant                              string
}

func (r *fakeReader) Volume(ctx context.Context, q VolumeQuery) ([]domain.VolumePoint, error) {
	r.volumeCalls++
	r.lastVolume = q
	return r.volume, r.err
}

func (r *fakeReader) Revenue(ctx context.Context, q RevenueQuery) ([]domain.RevenueSummary, error) {
	r.revenueCalls++
	r.lastRevenue = q
	return r.revenue, r.err
}

func (r *fakeReader) Balances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error) {
	r.balanceCalls++
	r.lastTenant = tenantID
	return r.balances, r.err
}
