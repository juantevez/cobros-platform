// Package tenant provee el adaptador que verifica si un comercio puede cobrar.
package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/payment/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// statusActive es el estado del Tenant (contexto Auth) que habilita a cobrar:
// se alcanza al aprobarse el KYC en Onboarding, y se pierde al suspender.
const statusActive = "active"

// StatusGate implementa application.TenantGate consultando directamente la
// tabla tenants del contexto Auth (monolito modular; mismo patrón que Payout
// con las tablas de Ledger y Onboarding).
type StatusGate struct {
	pool pkgpostgres.Conn
}

func NewStatusGate(pool pkgpostgres.Conn) *StatusGate {
	return &StatusGate{pool: pool}
}

// EnsureCanProcessPayments retorna domain.ErrTenantNotActive si el tenant no
// existe o no está activo (pending = KYC sin aprobar, suspended).
func (g *StatusGate) EnsureCanProcessPayments(ctx context.Context, tenantID domain.TenantID) error {
	var status string
	err := g.pool.QueryRow(ctx,
		`SELECT status FROM tenants WHERE id = $1`, tenantID.String(),
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: tenant not found", domain.ErrTenantNotActive)
	}
	if err != nil {
		return fmt.Errorf("tenant gate: query status: %w", err)
	}
	if status != statusActive {
		return fmt.Errorf("%w: status %q", domain.ErrTenantNotActive, status)
	}
	return nil
}
