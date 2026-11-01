package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// TenantStatusTTL es cuánto tarda, como máximo, una suspensión (o una
// reactivación) en reflejarse en los requests con credenciales vigentes.
const TenantStatusTTL = 30 * time.Second

// TenantStatusChecker indica si un tenant puede seguir usando sus credenciales
// vigentes (JWT o API key). Lo usan los middlewares en cada request: el JWT
// dura 15 minutos y la API key no vence, y sin este chequeo una suspensión
// recién se notaba al pedir un token nuevo.
type TenantStatusChecker interface {
	// EnsureNotSuspended devuelve ErrTenantSuspended si el tenant está
	// suspendido y ErrTenantNotFound si no existe.
	EnsureNotSuspended(ctx context.Context, tenantID domain.TenantID) error
}

// CachedTenantStatus implementa TenantStatusChecker leyendo tenants con una
// caché en memoria de TenantStatusTTL por tenant, para no sumar una consulta
// por request. Cada réplica tiene la suya: el retraso máximo es el TTL.
type CachedTenantStatus struct {
	repo  TenantRepository
	clock Clock
	ttl   time.Duration

	mu      sync.Mutex
	entries map[domain.TenantID]tenantStatusEntry
}

type tenantStatusEntry struct {
	suspended bool
	expires   time.Time
}

func NewCachedTenantStatus(repo TenantRepository, clock Clock, ttl time.Duration) *CachedTenantStatus {
	return &CachedTenantStatus{repo: repo, clock: clock, ttl: ttl, entries: map[domain.TenantID]tenantStatusEntry{}}
}

func (c *CachedTenantStatus) EnsureNotSuspended(ctx context.Context, tenantID domain.TenantID) error {
	now := c.clock.Now()
	c.mu.Lock()
	e, ok := c.entries[tenantID]
	c.mu.Unlock()

	if !ok || !now.Before(e.expires) {
		tenant, err := c.repo.FindByID(ctx, tenantID)
		if err != nil {
			if errors.Is(err, domain.ErrTenantNotFound) {
				return domain.ErrTenantNotFound
			}
			return fmt.Errorf("tenant status: %w", err)
		}
		e = tenantStatusEntry{suspended: tenant.Status() == domain.TenantStatusSuspended, expires: now.Add(c.ttl)}
		c.mu.Lock()
		c.entries[tenantID] = e
		c.mu.Unlock()
	}

	if e.suspended {
		return domain.ErrTenantSuspended
	}
	return nil
}
