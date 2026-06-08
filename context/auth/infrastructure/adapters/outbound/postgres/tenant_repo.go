package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// pgTenantRepository implementa application.TenantRepository sobre PostgreSQL.
type pgTenantRepository struct {
	pool *pgxpool.Pool
}

// NewTenantRepository crea un repositorio de tenants.
func NewTenantRepository(pool *pgxpool.Pool) *pgTenantRepository {
	return &pgTenantRepository{pool: pool}
}

func (r *pgTenantRepository) Save(ctx context.Context, t *domain.Tenant) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO tenants (id, legal_name, status, environment, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID().String(),
		t.LegalName(),
		string(t.Status()),
		string(t.Environment()),
		t.CreatedAt(),
		t.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("tenant repo: save: %w", err)
	}
	return nil
}

func (r *pgTenantRepository) Update(ctx context.Context, t *domain.Tenant) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	tag, err := conn.Exec(ctx, `
		UPDATE tenants
		SET status = $2, environment = $3, updated_at = $4
		WHERE id = $1`,
		t.ID().String(),
		string(t.Status()),
		string(t.Environment()),
		t.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("tenant repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTenantNotFound
	}
	return nil
}

func (r *pgTenantRepository) FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, legal_name, status, environment, created_at, updated_at
		FROM tenants
		WHERE id = $1`,
		id.String(),
	)

	return scanTenant(row)
}

// scanTenant escanea una fila de la tabla tenants y reconstituye el agregado.
func scanTenant(row pgx.Row) (*domain.Tenant, error) {
	var (
		idStr, legalName, status, env string
		createdAt, updatedAt          time.Time
	)

	if err := row.Scan(&idStr, &legalName, &status, &env, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTenantNotFound
		}
		return nil, fmt.Errorf("tenant repo: scan: %w", err)
	}

	return domain.ReconstituteTenant(
		domain.TenantID(idStr),
		legalName,
		domain.TenantStatus(status),
		domain.Environment(env),
		createdAt.UTC(),
		updatedAt.UTC(),
	), nil
}
