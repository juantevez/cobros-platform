package application

import (
	"context"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

var errBoom = errors.New("boom")

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeRepo implementa PaymentRepository en memoria. Registra el estado del
// pago en cada Update para poder verificar la secuencia de transiciones.
type fakeRepo struct {
	saved       []*domain.Payment
	updates     []domain.PaymentStatus
	byID        map[domain.PaymentID]*domain.Payment
	byKey       *domain.Payment
	saveErr     error
	updateErr   error
	updateErrAt int // si > 0, sólo falla el Update número N (1-based)
	findErr     error
	findKeyErr  error
	// claimLost simula que otra ejecución ya reclamó el pago (ClaimProcessing → false).
	claimLost bool

	// capturados para aserciones
	keyLookups    int
	lastKey       string
	lastKeyTenant domain.TenantID
}

func (r *fakeRepo) Save(ctx context.Context, p *domain.Payment) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, p)
	return nil
}

func (r *fakeRepo) Update(ctx context.Context, p *domain.Payment) error {
	r.updates = append(r.updates, p.Status())
	if r.updateErr != nil && (r.updateErrAt == 0 || r.updateErrAt == len(r.updates)) {
		return r.updateErr
	}
	return nil
}

// ClaimProcessing registra la transición como un Update más, para que las
// aserciones sobre la secuencia de estados sigan valiendo.
func (r *fakeRepo) ClaimProcessing(ctx context.Context, p *domain.Payment) (bool, error) {
	r.updates = append(r.updates, p.Status())
	if r.updateErr != nil && (r.updateErrAt == 0 || r.updateErrAt == len(r.updates)) {
		return false, r.updateErr
	}
	return !r.claimLost, nil
}

func (r *fakeRepo) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPaymentNotFound
	}
	return p, nil
}

func (r *fakeRepo) FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payment, error) {
	r.keyLookups++
	r.lastKey = key
	r.lastKeyTenant = tenantID
	if r.findKeyErr != nil {
		return nil, r.findKeyErr
	}
	if r.byKey == nil {
		return nil, domain.ErrPaymentNotFound
	}
	return r.byKey, nil
}

// fakePSP captura las requests y devuelve resultados o errores configurados.
type fakePSP struct {
	captureResult PSPCaptureResult
	captureErr    error
	refundResult  PSPRefundResult
	refundErr     error
	captures      []PSPCaptureRequest
	refunds       []PSPRefundRequest
}

func (p *fakePSP) AuthorizeAndCapture(ctx context.Context, req PSPCaptureRequest) (PSPCaptureResult, error) {
	p.captures = append(p.captures, req)
	if p.captureErr != nil {
		return PSPCaptureResult{}, p.captureErr
	}
	return p.captureResult, nil
}

func (p *fakePSP) Refund(ctx context.Context, req PSPRefundRequest) (PSPRefundResult, error) {
	p.refunds = append(p.refunds, req)
	if p.refundErr != nil {
		return PSPRefundResult{}, p.refundErr
	}
	return p.refundResult, nil
}

func (p *fakePSP) Name() string { return "fakepsp" }

// fakeRouter devuelve siempre el mismo PSP o un error.
type fakeRouter struct {
	psp        PSPAdapter
	err        error
	calls      int
	lastMethod domain.PaymentMethod
	lastTenant domain.TenantID
}

func (r *fakeRouter) Route(ctx context.Context, method domain.PaymentMethod, tenantID domain.TenantID) (PSPAdapter, error) {
	r.calls++
	r.lastMethod = method
	r.lastTenant = tenantID
	if r.err != nil {
		return nil, r.err
	}
	return r.psp, nil
}

// fakeGate habilita o rechaza al tenant.
type fakeGate struct {
	err   error
	calls int
}

func (g *fakeGate) EnsureCanProcessPayments(ctx context.Context, tenantID domain.TenantID) error {
	g.calls++
	return g.err
}

// fakeRisk devuelve una decisión fija o un error.
type fakeRisk struct {
	decision   RiskDecision
	err        error
	calls      int
	lastStatus domain.PaymentStatus
}

func (r *fakeRisk) Evaluate(ctx context.Context, p *domain.Payment) (RiskDecision, error) {
	r.calls++
	r.lastStatus = p.Status()
	if r.err != nil {
		return RiskDecision{}, r.err
	}
	return r.decision, nil
}

// fakeFees devuelve una comisión fija o un error.
type fakeFees struct {
	fee        int64
	err        error
	calls      int
	lastAmount domain.Money
	lastMethod domain.PaymentMethod
}

func (f *fakeFees) Calculate(ctx context.Context, tenantID domain.TenantID, amount domain.Money, method domain.PaymentMethod) (domain.Money, error) {
	f.calls++
	f.lastAmount = amount
	f.lastMethod = method
	if f.err != nil {
		return domain.Money{}, f.err
	}
	return domain.ReconstituteMoney(f.fee, amount.Currency()), nil
}

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

var testTime = time.Date(2026, 10, 1, 15, 30, 0, 0, time.UTC)

func testPayer() domain.PayerInfo {
	return domain.PayerInfo{Name: "Ana Pérez", Email: "ana@example.com", DocType: "DNI", DocNum: "30123456"}
}

// buildPayment reconstituye un pago con el estado dado y comisiones fijas.
func buildPayment(tenantID string, status domain.PaymentStatus) *domain.Payment {
	captured := testTime.Add(time.Minute)
	return domain.ReconstitutePayment(
		domain.NewPaymentID(), domain.TenantID(tenantID),
		"idem-1", "",
		domain.ReconstituteMoney(10_000, "ARS"),
		domain.ReconstituteMoney(300, "ARS"),
		domain.ReconstituteMoney(150, "ARS"),
		testPayer(), domain.MethodCard,
		"fakepsp", "PSP-REF-1",
		status, "", nil,
		nil, &captured, nil,
		testTime, captured,
	)
}
