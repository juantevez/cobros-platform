// Package tenant provee el adaptador que verifica si un comercio puede desembolsar.
package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// PayoutGate implementa application.PayoutGate leyendo la tabla tenants y los
// holds de cumplimiento del contexto Auth (lectura cruzada, mismo patrón que
// el TenantGate de Payment).
type PayoutGate struct {
	pool pkgpostgres.Conn
}

func NewPayoutGate(pool pkgpostgres.Conn) *PayoutGate {
	return &PayoutGate{pool: pool}
}

// EnsurePayoutsAllowed retorna domain.ErrPayoutsNotAllowed si el tenant no
// existe, no está activo o tiene un hold de cumplimiento vigente.
func (g *PayoutGate) EnsurePayoutsAllowed(ctx context.Context, tenantID domain.TenantID) error {
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
		return fmt.Errorf("%w: tenant not found", domain.ErrPayoutsNotAllowed)
	}
	if err != nil {
		return fmt.Errorf("payout gate: query status: %w", err)
	}
	if status != "active" {
		return fmt.Errorf("%w: status %q", domain.ErrPayoutsNotAllowed, status)
	}
	if onHold {
		return fmt.Errorf("%w: account on hold", domain.ErrPayoutsNotAllowed)
	}
	return nil
}
