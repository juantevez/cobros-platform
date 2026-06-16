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

type pgPlanRepository struct {
	pool *pgxpool.Pool
}

func NewPlanRepository(pool *pgxpool.Pool) *pgPlanRepository {
	return &pgPlanRepository{pool: pool}
}

// Save inserta un plan nuevo junto con sus method rates.
func (r *pgPlanRepository) Save(ctx context.Context, p *domain.PricingPlan) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	if _, err := conn.Exec(ctx, `
		INSERT INTO billing_plans
			(id, name, description, base_rate_bps, base_fixed_amount,
			 monthly_fee, currency, active, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		p.ID().String(), p.Name(), p.Description(),
		p.BaseRateBps(), p.BaseFixedAmount(),
		p.MonthlyFee(), p.Currency(), p.Active(),
		p.CreatedAt(), p.UpdatedAt(),
	); err != nil {
		return fmt.Errorf("plan repo: save: %w", err)
	}

	return r.upsertMethodRates(ctx, conn, p)
}

// Update actualiza el estado del plan (active/inactive) y sus method rates.
func (r *pgPlanRepository) Update(ctx context.Context, p *domain.PricingPlan) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	if _, err := conn.Exec(ctx, `
		UPDATE billing_plans
		SET base_rate_bps=$2, base_fixed_amount=$3, monthly_fee=$4,
		    active=$5, updated_at=$6
		WHERE id=$1`,
		p.ID().String(),
		p.BaseRateBps(), p.BaseFixedAmount(), p.MonthlyFee(),
		p.Active(), p.UpdatedAt(),
	); err != nil {
		return fmt.Errorf("plan repo: update: %w", err)
	}

	return r.upsertMethodRates(ctx, conn, p)
}

func (r *pgPlanRepository) FindByID(ctx context.Context, id domain.PlanID) (*domain.PricingPlan, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx,
		`SELECT id, name, description, base_rate_bps, base_fixed_amount,
		        monthly_fee, currency, active, created_at, updated_at
		 FROM billing_plans WHERE id=$1`, id.String())

	return r.scanWithRates(ctx, conn, row)
}

func (r *pgPlanRepository) ListActive(ctx context.Context) ([]*domain.PricingPlan, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	rows, err := conn.Query(ctx,
		`SELECT id, name, description, base_rate_bps, base_fixed_amount,
		        monthly_fee, currency, active, created_at, updated_at
		 FROM billing_plans WHERE active=true ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("plan repo: list: %w", err)
	}
	defer rows.Close()

	var plans []*domain.PricingPlan
	for rows.Next() {
		p, err := r.scanWithRates(ctx, conn, rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *pgPlanRepository) scanWithRates(ctx context.Context, conn postgres.Conn, row interface{ Scan(...any) error }) (*domain.PricingPlan, error) {
	var (
		idStr, name, desc, currency string
		baseRateBps, baseFixed, monthly int64
		active                      bool
		createdAt, updatedAt        time.Time
	)
	if err := row.Scan(&idStr, &name, &desc, &baseRateBps, &baseFixed,
		&monthly, &currency, &active, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlanNotFound
		}
		return nil, fmt.Errorf("plan repo: scan: %w", err)
	}

	// Cargar method rates.
	rateRows, err := conn.Query(ctx,
		`SELECT method, rate_bps, fixed_amount
		 FROM billing_plan_method_rates WHERE plan_id=$1`, idStr)
	if err != nil {
		return nil, fmt.Errorf("plan repo: load method rates: %w", err)
	}
	defer rateRows.Close()

	methodRates := make(map[domain.PaymentMethod]domain.MethodRate)
	for rateRows.Next() {
		var method string
		var rateBps, fixedAmt int64
		if err := rateRows.Scan(&method, &rateBps, &fixedAmt); err != nil {
			return nil, fmt.Errorf("plan repo: scan method rate: %w", err)
		}
		m, _ := domain.ParsePaymentMethod(method)
		methodRates[m] = domain.MethodRate{RateBps: rateBps, FixedAmount: fixedAmt}
	}

	return domain.ReconstitutePricingPlan(
		domain.PlanID(idStr), name, desc,
		baseRateBps, baseFixed, monthly,
		methodRates, currency, active,
		createdAt.UTC(), updatedAt.UTC(),
	), nil
}

func (r *pgPlanRepository) upsertMethodRates(ctx context.Context, conn postgres.Conn, p *domain.PricingPlan) error {
	for method, mr := range p.MethodRates() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO billing_plan_method_rates (plan_id, method, rate_bps, fixed_amount)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (plan_id, method)
			DO UPDATE SET rate_bps=$3, fixed_amount=$4`,
			p.ID().String(), method.String(), mr.RateBps, mr.FixedAmount,
		); err != nil {
			return fmt.Errorf("plan repo: upsert method rate %q: %w", method, err)
		}
	}
	return nil
}
