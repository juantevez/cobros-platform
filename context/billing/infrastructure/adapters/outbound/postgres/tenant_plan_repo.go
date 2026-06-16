package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/billing/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgTenantPlanRepository struct {
	pool *pgxpool.Pool
}

func NewTenantPlanRepository(pool *pgxpool.Pool) *pgTenantPlanRepository {
	return &pgTenantPlanRepository{pool: pool}
}

func (r *pgTenantPlanRepository) Save(ctx context.Context, tp *domain.TenantPlan) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO billing_tenant_plans
			(id, tenant_id, plan_id, plan_name,
			 custom_rate_bps, custom_fixed_amount,
			 active, valid_from, valid_until, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tp.ID().String(), tp.TenantID().String(),
		tp.PlanID().String(), tp.PlanName(),
		tp.CustomRateBps(), tp.CustomFixedAmount(),
		tp.Active(), tp.ValidFrom(), tp.ValidUntil(),
		tp.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("tenant plan repo: save: %w", err)
	}
	return nil
}

func (r *pgTenantPlanRepository) Update(ctx context.Context, tp *domain.TenantPlan) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE billing_tenant_plans
		SET active=$2, valid_until=$3
		WHERE id=$1`,
		tp.ID().String(), tp.Active(), tp.ValidUntil(),
	)
	if err != nil {
		return fmt.Errorf("tenant plan repo: update: %w", err)
	}
	return nil
}

func (r *pgTenantPlanRepository) FindActive(ctx context.Context, tenantID domain.TenantID) (*domain.TenantPlan, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseTPSelect+
		` WHERE tenant_id=$1 AND active=true
		  ORDER BY valid_from DESC LIMIT 1`,
		tenantID.String())
	return scanTenantPlan(row)
}

func (r *pgTenantPlanRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.TenantPlan, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	rows, err := conn.Query(ctx, baseTPSelect+
		` WHERE tenant_id=$1 ORDER BY valid_from DESC`,
		tenantID.String())
	if err != nil {
		return nil, fmt.Errorf("tenant plan repo: list: %w", err)
	}
	defer rows.Close()

	var tps []*domain.TenantPlan
	for rows.Next() {
		tp, err := scanTenantPlan(rows)
		if err != nil {
			return nil, err
		}
		tps = append(tps, tp)
	}
	return tps, rows.Err()
}

const baseTPSelect = `
	SELECT id, tenant_id, plan_id, plan_name,
	       custom_rate_bps, custom_fixed_amount,
	       active, valid_from, valid_until, created_at
	FROM billing_tenant_plans`

func scanTenantPlan(row interface{ Scan(...any) error }) (*domain.TenantPlan, error) {
	var (
		idStr, tenantIDStr, planIDStr, planName string
		customRateBps, customFixed              *int64
		active                                  bool
		validFrom, createdAt                    time.Time
		validUntil                              *time.Time
	)
	if err := row.Scan(
		&idStr, &tenantIDStr, &planIDStr, &planName,
		&customRateBps, &customFixed,
		&active, &validFrom, &validUntil, &createdAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTenantPlanNotFound
		}
		return nil, fmt.Errorf("tenant plan repo: scan: %w", err)
	}

	return domain.ReconstituteTenantPlan(
		domain.TenantPlanID(idStr),
		domain.TenantID(tenantIDStr),
		domain.PlanID(planIDStr),
		planName,
		customRateBps, customFixed,
		active, validFrom.UTC(), validUntil, createdAt.UTC(),
	), nil
}
