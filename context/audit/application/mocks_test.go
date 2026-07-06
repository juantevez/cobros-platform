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

	// capturados para aserciones
	lastLimit    int
	lastTenantID string
	lastFromID   int64
}

func (r *fakeRepo) Save(ctx context.Context, entry *domain.AuditLogEntry) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, entry)
	return nil
}

func (r *fakeRepo) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) {
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

func (r *fakeRepo) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	r.lastFromID = fromID
	r.lastLimit = limit
	if r.fromIDErr != nil {
		return nil, r.fromIDErr
	}
	return r.fromID, nil
}

// sha256Hasher es la implementación real de HashComputer para los tests.
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
	e, _ := domain.NewAuditLogEntry(
		tenantID, "actor", action, rt, resourceID,
		nil, "", prevHash, testNow, newHasher().Compute,
	)
	return e
}
