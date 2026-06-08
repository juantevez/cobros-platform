package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// pgAuditLogRepository implementa AuditLogRepository sobre PostgreSQL.
//
// La tabla audit_log no usa RLS (es un log global). El aislamiento por
// tenant se aplica en las queries con WHERE tenant_id = $1.
//
// Las inserciones se serializan con SELECT ... FOR UPDATE sobre el último
// registro para garantizar la integridad del hash chain ante múltiples
// instancias del worker.
type pgAuditLogRepository struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepository(pool *pgxpool.Pool) *pgAuditLogRepository {
	return &pgAuditLogRepository{pool: pool}
}

// Save inserta una nueva entrada en el log.
// El ID (BIGSERIAL) es asignado por PostgreSQL y se actualiza en la entidad.
func (r *pgAuditLogRepository) Save(ctx context.Context, entry *domain.AuditLogEntry) error {
	metadata, err := json.Marshal(entry.Metadata())
	if err != nil {
		return fmt.Errorf("audit repo: marshal metadata: %w", err)
	}

	var tenantID *string
	if t := entry.TenantID(); t != "" {
		tenantID = &t
	}

	// El RETURNING id permite que el dominio conozca su ID asignado,
	// aunque en Fase 1 no lo necesitamos de vuelta.
	_, err = r.pool.Exec(ctx, `
		INSERT INTO audit_log
			(tenant_id, actor, action, resource_type, resource_id,
			 metadata, correlation_id, prev_hash, hash, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tenantID,
		entry.Actor(),
		entry.Action().String(),
		entry.ResourceType().String(),
		nullableStr(entry.ResourceID()),
		metadata,
		nullableStr(entry.CorrelationID()),
		nullableBytes(entry.PrevHash()),
		entry.Hash(),
		entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("audit repo: save: %w", err)
	}
	return nil
}

// FindLast retorna el último registro insertado para encadenar el hash.
// Usa SELECT FOR UPDATE para serializar inserciones concurrentes.
func (r *pgAuditLogRepository) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, actor, action, resource_type, resource_id,
		       metadata, correlation_id, prev_hash, hash, created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT 1
		FOR UPDATE`)

	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // tabla vacía: primer registro
		}
		return nil, fmt.Errorf("audit repo: find last: %w", err)
	}
	return entry, nil
}

func (r *pgAuditLogRepository) ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, actor, action, resource_type, resource_id,
		       metadata, correlation_id, prev_hash, hash, created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("audit repo: list recent: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (r *pgAuditLogRepository) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, actor, action, resource_type, resource_id,
		       metadata, correlation_id, prev_hash, hash, created_at
		FROM audit_log
		WHERE tenant_id = $1
		ORDER BY id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("audit repo: list by tenant: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (r *pgAuditLogRepository) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, actor, action, resource_type, resource_id,
		       metadata, correlation_id, prev_hash, hash, created_at
		FROM audit_log
		WHERE id >= $1
		ORDER BY id ASC
		LIMIT $2`, fromID, limit)
	if err != nil {
		return nil, fmt.Errorf("audit repo: list from id: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanEntry(row pgx.Row) (*domain.AuditLogEntry, error) {
	var (
		id                                               int64
		tenantID, resourceID, correlationID              *string
		actor, action, resourceType                      string
		metadataJSON                                     []byte
		prevHash, hash                                   []byte
		createdAt                                        time.Time
	)

	if err := row.Scan(
		&id, &tenantID, &actor, &action, &resourceType, &resourceID,
		&metadataJSON, &correlationID, &prevHash, &hash, &createdAt,
	); err != nil {
		return nil, err
	}

	var metadata map[string]string
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	act, _ := domain.ParseAction(action)
	rt, _ := domain.ParseResourceType(resourceType)

	return domain.ReconstituteAuditLogEntry(
		id,
		deref(tenantID),
		actor,
		act,
		rt,
		deref(resourceID),
		metadata,
		deref(correlationID),
		prevHash,
		hash,
		createdAt.UTC(),
	), nil
}

func scanEntries(rows pgx.Rows) ([]*domain.AuditLogEntry, error) {
	var entries []*domain.AuditLogEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("audit repo: scan: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
