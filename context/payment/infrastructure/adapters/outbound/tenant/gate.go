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
// tabla tenants del contexto Auth y sus holds de cumplimiento (monolito
// modular; mismo patrón que Payout con las tablas de Ledger y Onboarding).
type StatusGate struct {
	pool pkgpostgres.Conn
}

func NewStatusGate(pool pkgpostgres.Conn) *StatusGate {
	return &StatusGate{pool: pool}
}

// EnsureCanProcessPayments retorna domain.ErrTenantNotActive si el tenant no
// existe, no está activo (pending = KYC sin aprobar, suspended) o tiene un
// hold preventivo de cumplimiento vigente. El error expuesto al comercio es
// el mismo en todos los casos: no revela el motivo del hold (tipping-off).
func (g *StatusGate) EnsureCanProcessPayments(ctx context.Context, tenantID domain.TenantID) error {
	var (
		status string
		onHold bool
	)
	err := g.pool.QueryRow(ctx, `
		SELECT t.status,
		       EXISTS (SELECT 1 FROM tenant_compliance_holds h
		               WHERE h.tenant_id = t.id AND h.released_at IS NULL)
		FROM tenants t WHERE t.id = $1`, tenantID.String(),
	).Scan(&status, &onHold)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: tenant not found", domain.ErrTenantNotActive)
	}
	if err != nil {
		return fmt.Errorf("tenant gate: query status: %w", err)
	}
	if status != statusActive {
		return fmt.Errorf("%w: status %q", domain.ErrTenantNotActive, status)
	}
	if onHold {
		return fmt.Errorf("%w: account on hold", domain.ErrTenantNotActive)
	}
	return nil
}
