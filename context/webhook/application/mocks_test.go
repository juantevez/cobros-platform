package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

var errBoom = errors.New("boom")

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ── EndpointRepository ────────────────────────────────────────────────────────

type fakeEndpointRepo struct {
	byID      map[domain.EndpointID]*domain.WebhookEndpoint
	active    []*domain.WebhookEndpoint
	byTenant  []*domain.WebhookEndpoint
	saved     []*domain.WebhookEndpoint
	updated   []*domain.WebhookEndpoint
	saveErr   error
	updateErr error
	findErr   error
	activeErr error
	tenantErr error

	// capturados para aserciones
	lastTenant    domain.TenantID
	lastEventType string
}

func newEndpointRepo(seed ...*domain.WebhookEndpoint) *fakeEndpointRepo {
	r := &fakeEndpointRepo{byID: map[domain.EndpointID]*domain.WebhookEndpoint{}}
	for _, e := range seed {
		r.byID[e.ID()] = e
	}
	return r
}

func (r *fakeEndpointRepo) Save(ctx context.Context, e *domain.WebhookEndpoint) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, e)
	return nil
}

func (r *fakeEndpointRepo) Update(ctx context.Context, e *domain.WebhookEndpoint) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, e)
	return nil
}

func (r *fakeEndpointRepo) FindByID(ctx context.Context, id domain.EndpointID) (*domain.WebhookEndpoint, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	e, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrEndpointNotFound
	}
	return e, nil
}

func (r *fakeEndpointRepo) FindByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.WebhookEndpoint, error) {
	r.lastTenant = tenantID
	return r.byTenant, r.tenantErr
}

func (r *fakeEndpointRepo) FindActiveByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) ([]*domain.WebhookEndpoint, error) {
	r.lastTenant, r.lastEventType = tenantID, eventType
	return r.active, r.activeErr
}

// ── DeliveryRepository ────────────────────────────────────────────────────────

type idempotencyKey struct {
	endpointID domain.EndpointID
	eventID    string
}

type claimCall struct {
	tenant       string // tenant del contexto (RLS)
	id           domain.DeliveryID
	attemptCount int
	now, until   time.Time
}

type fakeDeliveryRepo struct {
	mu         sync.Mutex
	byID       map[domain.DeliveryID]*domain.WebhookDelivery
	existing   map[idempotencyKey]*domain.WebhookDelivery
	list       []*domain.WebhookDelivery
	due        []*domain.WebhookDelivery
	saved      []*domain.WebhookDelivery
	updates    []domain.DeliveryStatus
	saveErrFor map[domain.EndpointID]error // Save falla sólo para esos endpoints
	idemErrFor map[domain.EndpointID]error // FindByEventAndEndpoint falla sólo para esos
	updateErr  error
	// Claim / Update: reclamos pedidos, deliveries que no se pueden reclamar
	// y attempt_count esperado en cada Update.
	claims       []claimCall
	notClaimable map[domain.DeliveryID]bool
	claimErr     error
	prevAttempts []int
	// updatedInTx: el último Update corrió dentro de fakeTx.RunInTx.
	updatedInTx bool
	findErr     error
	listErr     error
	dueErr      error

	// capturados para aserciones
	lastTenant domain.TenantID
	lastLimit  int
	dueCalls   int
	lastDueNow time.Time
}

func newDeliveryRepo(seed ...*domain.WebhookDelivery) *fakeDeliveryRepo {
	r := &fakeDeliveryRepo{
		byID:     map[domain.DeliveryID]*domain.WebhookDelivery{},
		existing: map[idempotencyKey]*domain.WebhookDelivery{},
	}
	for _, d := range seed {
		r.byID[d.ID()] = d
	}
	return r
}

func (r *fakeDeliveryRepo) Save(ctx context.Context, d *domain.WebhookDelivery) error {
	if err := r.saveErrFor[d.EndpointID()]; err != nil {
		return err
	}
	r.saved = append(r.saved, d)
	return nil
}

// Claim reclama salvo que notClaimable lo impida; registra el attemptCount
// con el que se pidió y el vencimiento.
func (r *fakeDeliveryRepo) Claim(ctx context.Context, id domain.DeliveryID, attemptCount int, now, until time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tenant, _ := pkgpostgres.TenantIDFromContext(ctx)
	r.claims = append(r.claims, claimCall{tenant: tenant, id: id, attemptCount: attemptCount, now: now, until: until})
	if r.claimErr != nil {
		return false, r.claimErr
	}
	return !r.notClaimable[id], nil
}

func (r *fakeDeliveryRepo) Update(ctx context.Context, d *domain.WebhookDelivery, prevAttemptCount int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, d.Status())
	r.prevAttempts = append(r.prevAttempts, prevAttemptCount)
	r.updatedInTx = inFakeTx(ctx)
	return r.updateErr
}

func (r *fakeDeliveryRepo) FindByID(ctx context.Context, id domain.DeliveryID) (*domain.WebhookDelivery, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDeliveryNotFound
	}
	return d, nil
}

func (r *fakeDeliveryRepo) FindByEventAndEndpoint(ctx context.Context, endpointID domain.EndpointID, eventID string) (*domain.WebhookDelivery, error) {
	if err := r.idemErrFor[endpointID]; err != nil {
		return nil, err
	}
	d, ok := r.existing[idempotencyKey{endpointID, eventID}]
	if !ok {
		return nil, domain.ErrDeliveryNotFound
	}
	return d, nil
}

func (r *fakeDeliveryRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.WebhookDelivery, error) {
	r.lastTenant, r.lastLimit = tenantID, limit
	return r.list, r.listErr
}

func (r *fakeDeliveryRepo) ListDueForRetry(ctx context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dueCalls++
	r.lastDueNow, r.lastLimit = now, limit
	return r.due, r.dueErr
}

func (r *fakeDeliveryRepo) dueCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dueCalls
}

// ── Otros puertos ─────────────────────────────────────────────────────────────

// fakeDispatcher devuelve un intento con el status HTTP configurado.
// Es seguro para uso concurrente (el RetryPoller despacha en paralelo).
// Si block != nil, cada Dispatch espera a que se cierre antes de responder.
type fakeDispatcher struct {
	mu        sync.Mutex
	status    int
	err       error
	endpoints []*domain.WebhookEndpoint
	delivered []*domain.WebhookDelivery
	block     chan struct{}
	inFlight  int
	maxFlight int
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, e *domain.WebhookEndpoint, d *domain.WebhookDelivery) (domain.DeliveryAttempt, error) {
	f.mu.Lock()
	f.endpoints = append(f.endpoints, e)
	f.delivered = append(f.delivered, d)
	f.inFlight++
	if f.inFlight > f.maxFlight {
		f.maxFlight = f.inFlight
	}
	block := f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return domain.NewDeliveryAttempt(d.AttemptCount()+1, f.status, "", "", 12), f.err
}

func (f *fakeDispatcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.delivered)
}

type fakeGuard struct {
	err  error
	urls []string
}

func (g *fakeGuard) Check(_ context.Context, rawURL string) error {
	g.urls = append(g.urls, rawURL)
	return g.err
}

type fakeSecretGen struct {
	secret string
	err    error
	calls  int
}

func (g *fakeSecretGen) Generate() (string, error) {
	g.calls++
	return g.secret, g.err
}

type fakeTx struct {
	mu    sync.Mutex // el poller despacha en paralelo
	calls int
	err   error
}

// fakeTxKey marca el contexto que fakeTx le pasa a fn, para que los fakes
// verifiquen que una escritura ocurrió dentro de la transacción.
type fakeTxKey struct{}

func inFakeTx(ctx context.Context) bool { return ctx.Value(fakeTxKey{}) != nil }

func (tx *fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	tx.mu.Lock()
	tx.calls++
	err := tx.err
	tx.mu.Unlock()
	if err != nil {
		return err
	}
	return fn(context.WithValue(ctx, fakeTxKey{}, true))
}

type fakePublisher struct {
	mu            sync.Mutex
	events        []domain.Event
	calls         int
	err           error
	publishedInTx bool
}

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.publishedInTx = inFakeTx(ctx)
	if p.err != nil {
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

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// ── Helpers ───────────────────────────────────────────────────────────────────

func newEndpoint(tenantID string, events ...string) *domain.WebhookEndpoint {
	if len(events) == 0 {
		events = []string{"payment.captured"}
	}
	e, _ := domain.NewWebhookEndpoint(domain.NewEndpointID(), domain.TenantID(tenantID),
		"https://merchant.example.com/hooks", "whsec_abcdef123456", events, "main")
	e.PullEvents()
	return e
}

// deliveryWith reconstituye una delivery en el estado y cantidad de intentos dados.
func deliveryWith(endpoint *domain.WebhookEndpoint, status domain.DeliveryStatus, attempts int) *domain.WebhookDelivery {
	next := testNow
	return domain.ReconstituteDelivery(
		domain.NewDeliveryID(), endpoint.ID(), endpoint.TenantID(),
		"payment.captured", "evt-1", []byte(`{"event_type":"payment.captured"}`),
		status, attempts, &next, nil, testNow.Add(-time.Hour), testNow.Add(-time.Hour),
	)
}
