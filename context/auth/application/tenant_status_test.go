package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

type countingTenantRepo struct {
	*fakeTenantRepo
	finds int
}

func (r *countingTenantRepo) FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error) {
	r.finds++
	return r.fakeTenantRepo.FindByID(ctx, id)
}

type movingClock struct{ now time.Time }

func (c *movingClock) Now() time.Time { return c.now }

func TestCachedTenantStatus(t *testing.T) {
	ctx := context.Background()
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	repo := &countingTenantRepo{fakeTenantRepo: newFakeTenantRepo(tenant)}
	clock := &movingClock{now: time.Unix(1_700_000_000, 0)}
	checker := NewCachedTenantStatus(repo, clock, 30*time.Second)

	if err := checker.EnsureNotSuspended(ctx, tenant.ID()); err != nil {
		t.Fatalf("active tenant: %v", err)
	}
	_ = tenant.Suspend("fraude")

	// Dentro del TTL responde de la caché, sin consultar la base.
	clock.now = clock.now.Add(10 * time.Second)
	if err := checker.EnsureNotSuspended(ctx, tenant.ID()); err != nil || repo.finds != 1 {
		t.Fatalf("cached: err=%v finds=%d", err, repo.finds)
	}

	// Vencido el TTL, la suspensión se nota.
	clock.now = clock.now.Add(30 * time.Second)
	if err := checker.EnsureNotSuspended(ctx, tenant.ID()); !errors.Is(err, domain.ErrTenantSuspended) {
		t.Fatalf("after ttl = %v, want ErrTenantSuspended", err)
	}

	if err := checker.EnsureNotSuspended(ctx, domain.NewTenantID()); !errors.Is(err, domain.ErrTenantNotFound) {
		t.Errorf("unknown tenant = %v", err)
	}

	repo.findErr = errors.New("db down")
	if err := checker.EnsureNotSuspended(ctx, domain.NewTenantID()); err == nil || errors.Is(err, domain.ErrTenantNotFound) {
		t.Errorf("repo error = %v", err)
	}
}
