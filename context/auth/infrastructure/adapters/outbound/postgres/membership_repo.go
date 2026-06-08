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

type pgMembershipRepository struct {
	pool *pgxpool.Pool
}

func NewMembershipRepository(pool *pgxpool.Pool) *pgMembershipRepository {
	return &pgMembershipRepository{pool: pool}
}

func (r *pgMembershipRepository) Save(ctx context.Context, m domain.Membership) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	var assignedBy *string
	if !m.AssignedBy().IsZero() {
		s := m.AssignedBy().String()
		assignedBy = &s
	}

	_, err := conn.Exec(ctx, `
		INSERT INTO memberships (user_id, tenant_id, role, assigned_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, tenant_id) DO NOTHING`,
		m.UserID().String(),
		m.TenantID().String(),
		m.Role().String(),
		assignedBy,
		m.CreatedAt(),
		m.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("membership repo: save: %w", err)
	}
	return nil
}

func (r *pgMembershipRepository) Update(ctx context.Context, m domain.Membership) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	var assignedBy *string
	if !m.AssignedBy().IsZero() {
		s := m.AssignedBy().String()
		assignedBy = &s
	}

	tag, err := conn.Exec(ctx, `
		UPDATE memberships
		SET role = $3, assigned_by = $4, updated_at = $5
		WHERE user_id = $1 AND tenant_id = $2`,
		m.UserID().String(),
		m.TenantID().String(),
		m.Role().String(),
		assignedBy,
		m.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("membership repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMembershipNotFound
	}
	return nil
}

func (r *pgMembershipRepository) FindByUserAndTenant(
	ctx context.Context,
	userID domain.UserID,
	tenantID domain.TenantID,
) (*domain.Membership, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT user_id, tenant_id, role, assigned_by, created_at, updated_at
		FROM memberships
		WHERE user_id = $1 AND tenant_id = $2`,
		userID.String(),
		tenantID.String(),
	)
	return scanMembership(row)
}

func (r *pgMembershipRepository) ListByTenant(
	ctx context.Context,
	tenantID domain.TenantID,
) ([]domain.Membership, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	rows, err := conn.Query(ctx, `
		SELECT user_id, tenant_id, role, assigned_by, created_at, updated_at
		FROM memberships
		WHERE tenant_id = $1
		ORDER BY created_at`,
		tenantID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("membership repo: list by tenant: %w", err)
	}
	defer rows.Close()

	var memberships []domain.Membership
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, *m)
	}
	return memberships, rows.Err()
}

// scanMembership funciona tanto con pgx.Row como con pgx.Rows porque ambos
// tienen el método Scan.
func scanMembership(row interface{ Scan(...any) error }) (*domain.Membership, error) {
	var (
		userIDStr, tenantIDStr, roleStr string
		assignedByStr                   *string
		createdAt, updatedAt            time.Time
	)

	if err := row.Scan(
		&userIDStr, &tenantIDStr, &roleStr,
		&assignedByStr, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrMembershipNotFound
		}
		return nil, fmt.Errorf("membership repo: scan: %w", err)
	}

	role, err := domain.ParseRole(roleStr)
	if err != nil {
		return nil, fmt.Errorf("membership repo: invalid role in db %q: %w", roleStr, err)
	}

	var assignedBy domain.UserID
	if assignedByStr != nil {
		assignedBy = domain.UserID(*assignedByStr)
	}

	m := domain.ReconstituteMembership(
		domain.UserID(userIDStr),
		domain.TenantID(tenantIDStr),
		role,
		assignedBy,
		createdAt.UTC(),
		updatedAt.UTC(),
	)
	return &m, nil
}
