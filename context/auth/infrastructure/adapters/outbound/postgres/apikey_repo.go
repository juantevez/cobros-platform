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

type pgApiKeyRepository struct {
	pool *pgxpool.Pool
}

func NewApiKeyRepository(pool *pgxpool.Pool) *pgApiKeyRepository {
	return &pgApiKeyRepository{pool: pool}
}

func (r *pgApiKeyRepository) Save(ctx context.Context, k *domain.ApiKey) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO api_keys
			(id, tenant_id, name, prefix, key_hash, environment, scopes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		k.ID().String(),
		k.TenantID().String(),
		k.Name(),
		k.Prefix(),
		k.KeyHash(),
		k.Environment().String(),
		scopeStrings(k.Scopes()),
		k.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("apikey repo: save: %w", err)
	}
	return nil
}

func (r *pgApiKeyRepository) Update(ctx context.Context, k *domain.ApiKey) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	tag, err := conn.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at = $2
		WHERE id = $1`,
		k.ID().String(),
		k.RevokedAt(),
	)
	if err != nil {
		return fmt.Errorf("apikey repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrApiKeyNotFound
	}
	return nil
}

func (r *pgApiKeyRepository) FindByID(ctx context.Context, id domain.ApiKeyID) (*domain.ApiKey, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, tenant_id, name, prefix, key_hash, environment, scopes, revoked_at, created_at
		FROM api_keys
		WHERE id = $1`,
		id.String(),
	)
	return scanApiKey(row)
}

// FindByPrefix busca una API key por su prefix visible.
// No usa ConnFromContext porque el middleware de auth llama esto sin tenant en contexto.
// El aislamiento se garantiza en el caso de uso comparando apiKey.TenantID().
func (r *pgApiKeyRepository) FindByPrefix(ctx context.Context, prefix string) (*domain.ApiKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, prefix, key_hash, environment, scopes, revoked_at, created_at
		FROM api_keys
		WHERE prefix = $1`,
		prefix,
	)
	return scanApiKey(row)
}

func scanApiKey(row pgx.Row) (*domain.ApiKey, error) {
	var (
		idStr, tenantIDStr, name, prefix, keyHash, envStr string
		scopeStrs                                         []string
		revokedAt                                         *time.Time
		createdAt                                         time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &name, &prefix, &keyHash,
		&envStr, &scopeStrs, &revokedAt, &createdAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrApiKeyNotFound
		}
		return nil, fmt.Errorf("apikey repo: scan: %w", err)
	}

	env, err := domain.ParseEnvironment(envStr)
	if err != nil {
		return nil, fmt.Errorf("apikey repo: invalid env in db: %w", err)
	}

	scopes := make([]domain.Scope, 0, len(scopeStrs))
	for _, s := range scopeStrs {
		sc, err := domain.ParseScope(s)
		if err != nil {
			return nil, fmt.Errorf("apikey repo: invalid scope in db %q: %w", s, err)
		}
		scopes = append(scopes, sc)
	}

	return domain.ReconstituteApiKey(
		domain.ApiKeyID(idStr),
		domain.TenantID(tenantIDStr),
		name,
		prefix,
		keyHash,
		env,
		scopes,
		revokedAt,
		createdAt.UTC(),
	), nil
}

// scopeStrings convierte []domain.Scope a []string para pgx arrays.
func scopeStrings(scopes []domain.Scope) []string {
	ss := make([]string, len(scopes))
	for i, s := range scopes {
		ss[i] = s.String()
	}
	return ss
}
