package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// pgComplianceHoldRepository implementa application.ComplianceHoldRepository
// sobre tenant_compliance_holds. Sin RLS (como tenants): es estado de
// plataforma sobre el comercio y lo leen los gates de Payment y Payout.
type pgComplianceHoldRepository struct {
	pool postgres.Conn
}

// NewComplianceHoldRepository crea el repositorio de holds de cumplimiento.
func NewComplianceHoldRepository(pool postgres.Conn) *pgComplianceHoldRepository {
	return &pgComplianceHoldRepository{pool: pool}
}

// Place crea el hold. ON CONFLICT DO NOTHING: una re-entrega no lo duplica y,
// si la alerta ya fue liberada (release llegó primero), no lo reactiva.
func (r *pgComplianceHoldRepository) Place(ctx context.Context, tenantID domain.TenantID, alertID string, at time.Time) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO tenant_compliance_holds (tenant_id, alert_id, placed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, alert_id) DO NOTHING`,
		tenantID.String(), alertID, at,
	)
	if err != nil {
		return fmt.Errorf("compliance hold repo: place: %w", err)
	}
	return nil
}

// Release levanta el hold. Es un upsert: si el place todavía no llegó, deja
// la fila ya liberada para que el place posterior sea un no-op.
func (r *pgComplianceHoldRepository) Release(ctx context.Context, tenantID domain.TenantID, alertID string, at time.Time) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO tenant_compliance_holds (tenant_id, alert_id, placed_at, released_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (tenant_id, alert_id)
		DO UPDATE SET released_at = COALESCE(tenant_compliance_holds.released_at, EXCLUDED.released_at)`,
		tenantID.String(), alertID, at,
	)
	if err != nil {
		return fmt.Errorf("compliance hold repo: release: %w", err)
	}
	return nil
}
