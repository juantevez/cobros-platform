package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

var errBoom = errors.New("boom")

// ── Fakes de puertos ──────────────────────────────────────────────────────────

// fakeRepo implementa AuditLogRepository en memoria. Los slices List* se
// devuelven tal cual se cargaron; cada operación puede forzar un error.
type fakeRepo struct {
	last        *domain.AuditLogEntry
	saved       []*domain.AuditLogEntry
	recent      []*domain.AuditLogEntry
	byTenant    []*domain.AuditLogEntry
	fromID      []*domain.AuditLogEntry
	findLastErr error
	saveErr     error
	recentErr   error
	tenantErr   error
	fromIDErr   error
	lockErr     error
	existsErr   error
	before      *domain.AuditLogEntry // predecesora devuelta por FindBefore
	beforeErr   error

	// capturados para aserciones
	calls        []string // orden de LockChain / FindLast / Save
	lastLimit    int
	lastTenantID string
	lastFromID   int64
	lastBeforeID int64
}

func (r *fakeRepo) LockChain(ctx context.Context) error {
	r.calls = append(r.calls, "lock:"+txState(ctx))
	return r.lockErr
}

func (r *fakeRepo) ExistsByEventID(ctx context.Context, eventID string) (bool, error) {
	r.calls = append(r.calls, "exists:"+txState(ctx))
	if r.existsErr != nil {
		return false, r.existsErr
	}
	for _, e := range r.saved {
		if e.EventID() == eventID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) Save(ctx context.Context, entry *domain.AuditLogEntry) error {
	r.calls = append(r.calls, "save:"+txState(ctx))
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, entry)
	return nil
}

func (r *fakeRepo) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) {
	r.calls = append(r.calls, "find:"+txState(ctx))
	if r.findLastErr != nil {
		return nil, r.findLastErr
	}
	return r.last, nil
}

func (r *fakeRepo) ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error) {
	r.lastLimit = limit
	if r.recentErr != nil {
		return nil, r.recentErr
	}
	return r.recent, nil
}

func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error) {
	r.lastTenantID = tenantID
	r.lastLimit = limit
	if r.tenantErr != nil {
		return nil, r.tenantErr
	}
	return r.byTenant, nil
}

func (r *fakeRepo) FindBefore(ctx context.Context, beforeID int64) (*domain.AuditLogEntry, error) {
	r.lastBeforeID = beforeID
	return r.before, r.beforeErr
}

func (r *fakeRepo) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	r.lastFromID = fromID
	r.lastLimit = limit
	if r.fromIDErr != nil {
		return nil, r.fromIDErr
	}
	return r.fromID, nil
}

// sha256Hasher es la implementación real de HashComputer para los tests.
// fakeCheckpoints implementa CheckpointRepository en memoria.
type fakeCheckpoints struct {
	saved       []domain.ChainCheckpoint
	mismatch    *domain.CheckpointMismatch
	latestErr   error
	saveErr     error
	mismatchErr error
}

func (f *fakeCheckpoints) Save(ctx context.Context, cp domain.ChainCheckpoint) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, cp)
	return nil
}

func (f *fakeCheckpoints) Latest(ctx context.Context) (*domain.ChainCheckpoint, error) {
	if f.latestErr != nil || len(f.saved) == 0 {
		return nil, f.latestErr
	}
	cp := f.saved[len(f.saved)-1]
	return &cp, nil
}

func (f *fakeCheckpoints) FirstMismatch(ctx context.Context) (*domain.CheckpointMismatch, error) {
	return f.mismatch, f.mismatchErr
}

// fakeTx marca el contexto para que el repo fake registre si cada llamada
// ocurrió dentro de la transacción.
type fakeTx struct{ err error }

type inTxKey struct{}

func (t fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if t.err != nil {
		return t.err
	}
	return fn(context.WithValue(ctx, inTxKey{}, true))
}

func txState(ctx context.Context) string {
	if ctx.Value(inTxKey{}) == true {
		return "tx"
	}
	return "notx"
}

type sha256Hasher struct{}

func (sha256Hasher) Compute(payload string) []byte {
	sum := sha256.Sum256([]byte(payload))
	return sum[:]
}

// fixedClock devuelve siempre el mismo instante.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// ── Helpers ───────────────────────────────────────────────────────────────────

var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func newHasher() sha256Hasher { return sha256Hasher{} }
func newClock() fixedClock    { return fixedClock{t: testNow} }

// buildEntry construye una entrada real encadenada al prevHash dado.
func buildEntry(tenantID string, action domain.Action, rt domain.ResourceType, resourceID string, prevHash []byte) *domain.AuditLogEntry {
	e, _ := domain.NewAuditLogEntry(domain.EntryInput{TenantID: tenantID, Actor: "actor", Action: action, ResourceType: rt, ResourceID: resourceID}, prevHash, testNow, newHasher().Compute)
	return e
}
