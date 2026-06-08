package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

type pgRefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *pgRefreshTokenRepository {
	return &pgRefreshTokenRepository{pool: pool}
}

// Save persiste un nuevo refresh token.
// No usa ConnFromContext porque los refresh tokens se guardan fuera de la
// transacción del dominio (en authenticate.go no hay TxManager).
func (r *pgRefreshTokenRepository) Save(ctx context.Context, token application.RefreshToken) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens
			(id, user_id, tenant_id, token_hash, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		token.ID,
		token.UserID.String(),
		token.TenantID.String(),
		token.TokenHash,
		token.IssuedAt.UTC(),
		token.ExpiresAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("refresh token repo: save: %w", err)
	}
	return nil
}

// FindByHash busca un refresh token por el hash del secreto.
// No usa ConnFromContext: la búsqueda es por hash global, sin tenant.
func (r *pgRefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (*application.RefreshToken, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, tenant_id, token_hash, issued_at, expires_at, revoked_at, replaced_by
		FROM refresh_tokens
		WHERE token_hash = $1`,
		tokenHash,
	)

	var (
		id, userIDStr, tenantIDStr, hash string
		issuedAt, expiresAt              time.Time
		revokedAt                        *time.Time
		replacedBy                       *string
	)

	if err := row.Scan(
		&id, &userIDStr, &tenantIDStr, &hash,
		&issuedAt, &expiresAt, &revokedAt, &replacedBy,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound // usamos este error genérico como "not found"
		}
		return nil, fmt.Errorf("refresh token repo: find by hash: %w", err)
	}

	return &application.RefreshToken{
		ID:         id,
		UserID:     domain.UserID(userIDStr),
		TenantID:   domain.TenantID(tenantIDStr),
		TokenHash:  hash,
		IssuedAt:   issuedAt.UTC(),
		ExpiresAt:  expiresAt.UTC(),
		RevokedAt:  revokedAt,
		ReplacedBy: replacedBy,
	}, nil
}

// Revoke marca el token como revocado y registra su sucesor (puede ser vacío en logout).
func (r *pgRefreshTokenRepository) Revoke(ctx context.Context, tokenID string, replacedBy string) error {
	var replacedByPtr *string
	if replacedBy != "" {
		replacedByPtr = &replacedBy
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = $2, replaced_by = $3
		WHERE id = $1 AND revoked_at IS NULL`,
		tokenID,
		time.Now().UTC(),
		replacedByPtr,
	)
	if err != nil {
		return fmt.Errorf("refresh token repo: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Ya estaba revocado: idempotente, no es error.
		return nil
	}
	return nil
}
