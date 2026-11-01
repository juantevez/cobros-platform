package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

const pgErrUniqueViolation = "23505"

// pgUserRepository implementa application.UserRepository sobre PostgreSQL.
type pgUserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *pgUserRepository {
	return &pgUserRepository{pool: pool}
}

// Cada método corre en la sesión RLS del tenant que corresponde (withTenant):
// el del usuario al escribir, el pedido en FindByEmail y el del llamador en
// FindByID.

func (r *pgUserRepository) Save(ctx context.Context, u *domain.User) error {
	return withTenant(ctx, r.pool, u.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return saveUser(ctx, conn, u)
	})
}

func saveUser(ctx context.Context, conn postgres.Conn, u *domain.User) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID().String(),
		u.TenantID().String(),
		u.Email().String(),
		u.PasswordHash(),
		string(u.Status()),
		u.CreatedAt(),
		u.UpdatedAt(),
	)
	if err != nil {
		// Mapear violación de UNIQUE(tenant_id, email) al error de dominio.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			return domain.ErrEmailAlreadyExists
		}
		return fmt.Errorf("user repo: save: %w", err)
	}
	return nil
}

func (r *pgUserRepository) Update(ctx context.Context, u *domain.User) error {
	return withTenant(ctx, r.pool, u.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return updateUser(ctx, conn, u)
	})
}

func updateUser(ctx context.Context, conn postgres.Conn, u *domain.User) error {
	tag, err := conn.Exec(ctx, `
		UPDATE users
		SET password_hash = $2, status = $3, updated_at = $4
		WHERE id = $1`,
		u.ID().String(),
		u.PasswordHash(),
		string(u.Status()),
		u.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("user repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func (r *pgUserRepository) FindByID(ctx context.Context, id domain.UserID) (*domain.User, error) {
	var u *domain.User
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		u, err = scanUser(conn.QueryRow(ctx, `
			SELECT id, tenant_id, email, password_hash, status, created_at, updated_at
			FROM users
			WHERE id = $1`,
			id.String(),
		))
		return err
	})
	return u, err
}

func (r *pgUserRepository) FindByEmail(ctx context.Context, tenantID domain.TenantID, email domain.Email) (*domain.User, error) {
	var u *domain.User
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		u, err = scanUser(conn.QueryRow(ctx, `
			SELECT id, tenant_id, email, password_hash, status, created_at, updated_at
			FROM users
			WHERE tenant_id = $1 AND email = $2`,
			tenantID.String(),
			email.String(),
		))
		return err
	})
	return u, err
}

func scanUser(row pgx.Row) (*domain.User, error) {
	var (
		idStr, tenantIDStr, emailStr, passwordHash, status string
		createdAt, updatedAt                               time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &emailStr, &passwordHash,
		&status, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("user repo: scan: %w", err)
	}

	email, err := domain.NewEmail(emailStr)
	if err != nil {
		return nil, fmt.Errorf("user repo: invalid email in db: %w", err)
	}

	return domain.ReconstituteUser(
		domain.UserID(idStr),
		domain.TenantID(tenantIDStr),
		email,
		passwordHash,
		domain.UserStatus(status),
		createdAt.UTC(),
		updatedAt.UTC(),
	), nil
}
