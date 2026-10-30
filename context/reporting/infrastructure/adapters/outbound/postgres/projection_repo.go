// Package postgres implementa los puertos del contexto de Reporting sobre PostgreSQL.
package postgres

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// ProjectionRepository persiste los hechos inmutables del read-model.
// Todas las inserciones son idempotentes vía ON CONFLICT DO NOTHING: la
// re-entrega de un evento de JetStream no altera el estado proyectado.
type ProjectionRepository struct{ pool pkgpostgres.Conn }

func NewProjectionRepository(pool pkgpostgres.Conn) *ProjectionRepository {
	return &ProjectionRepository{pool: pool}
}

// SavePaymentFact inserta un hecho de pago. Idempotente por payment_id.
func (r *ProjectionRepository) SavePaymentFact(ctx context.Context, f domain.PaymentFact) error {
	return withTenant(ctx, r.pool, f.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO report_payment_fact
				(payment_id, tenant_id, currency, amount, platform_fee, psp_fee, payment_method, captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (payment_id) DO NOTHING`,
			f.PaymentID, f.TenantID, f.Currency, f.Amount,
			f.PlatformFee, f.PSPFee, f.PaymentMethod, f.CapturedAt,
		)
		if err != nil {
			return fmt.Errorf("reporting repo: save payment fact: %w", err)
		}
		return nil
	})
}

// SaveLedgerMovement inserta un movimiento del ledger.
// Idempotente por (entry_id, account_id, direction).
func (r *ProjectionRepository) SaveLedgerMovement(ctx context.Context, m domain.LedgerMovement) error {
	return withTenant(ctx, r.pool, m.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO report_ledger_movement
				(entry_id, account_id, direction, tenant_id, account_type, currency, amount, posted_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (entry_id, account_id, direction) DO NOTHING`,
			m.EntryID, m.AccountID, m.Direction, m.TenantID,
			m.AccountType, m.Currency, m.Amount, m.PostedAt,
		)
		if err != nil {
			return fmt.Errorf("reporting repo: save ledger movement: %w", err)
		}
		return nil
	})
}

// SavePaymentAdjustment inserta un reembolso o contracargo. Idempotente por
// (kind, source_id).
func (r *ProjectionRepository) SavePaymentAdjustment(ctx context.Context, a domain.PaymentAdjustment) error {
	return withTenant(ctx, r.pool, a.TenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO report_payment_adjustment
				(kind, source_id, payment_id, tenant_id, currency, amount, occurred_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (kind, source_id) DO NOTHING`,
			a.Kind.String(), a.SourceID, a.PaymentID, a.TenantID,
			a.Currency, a.Amount, a.OccurredAt,
		)
		if err != nil {
			return fmt.Errorf("reporting repo: save payment adjustment: %w", err)
		}
		return nil
	})
}
