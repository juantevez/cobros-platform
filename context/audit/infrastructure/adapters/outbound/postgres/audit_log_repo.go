package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/audit/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// pgxPool es el subconjunto de *pgxpool.Pool que usa el repositorio.
// Definirlo como interfaz permite inyectar un mock (pgxmock) en los tests;
// en producción se satisface con *pgxpool.Pool sin cambios en los callers.
type pgxPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pgAuditLogRepository implementa AuditLogRepository sobre PostgreSQL.
//
// La tabla audit_log no usa RLS (es un log global). El aislamiento por
// tenant se aplica en las queries con WHERE tenant_id = $1.
//
// Las inserciones se serializan con un advisory lock transaccional
// (LockChain) tomado antes de leer el último hash; Save, FindLast y
// LockChain usan la transacción del contexto (RunInTx).
type pgAuditLogRepository struct {
	pool pgxPool
}

func NewAuditLogRepository(pool pgxPool) *pgAuditLogRepository {
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
	_, err = pkgpostgres.ConnFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO audit_log
			(tenant_id, actor, action, resource_type, resource_id,
			 metadata, correlation_id, event_id, occurred_at, prev_hash, hash, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		tenantID,
		entry.Actor(),
		entry.Action().String(),
		entry.ResourceType().String(),
		nullableStr(entry.ResourceID()),
		metadata,
		nullableStr(entry.CorrelationID()),
		nullableStr(entry.EventID()),
		entry.OccurredAt(),
		nullableBytes(entry.PrevHash()),
		entry.Hash(),
		entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("audit repo: save: %w", err)
	}
	return nil
}

// chainLockKey identifica el advisory lock de la cadena (uno global).
const chainLockKey = "audit_log_chain"

// LockChain toma un advisory lock transaccional: se libera solo al terminar la
// transacción, así que cubre la lectura del último hash y el INSERT. Fuera de
// una transacción no tendría efecto (se liberaría al terminar la sentencia).
func (r *pgAuditLogRepository) LockChain(ctx context.Context) error {
	if _, err := pkgpostgres.ConnFromContext(ctx, r.pool).Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, chainLockKey); err != nil {
		return fmt.Errorf("audit repo: lock chain: %w", err)
	}
	return nil
}

// ExistsByEventID indica si el evento ya tiene entrada (idempotencia ante
// re-entregas). Corre en la tx del contexto, después de LockChain.
func (r *pgAuditLogRepository) ExistsByEventID(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	if err := pkgpostgres.ConnFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM audit_log WHERE event_id = $1)`, eventID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("audit repo: exists by event id: %w", err)
	}
	return exists, nil
}

// FindLast retorna el último registro insertado para encadenar el hash.
// La serialización la da LockChain (tomado antes, en la misma tx).
func (r *pgAuditLogRepository) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) {
	row := pkgpostgres.ConnFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id, tenant_id, actor, action, resource_type, resource_id,
		       metadata, correlation_id, event_id, occurred_at, prev_hash, hash, created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT 1`)

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
		       metadata, correlation_id, event_id, occurred_at, prev_hash, hash, created_at
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
		       metadata, correlation_id, event_id, occurred_at, prev_hash, hash, created_at
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
		       metadata, correlation_id, event_id, occurred_at, prev_hash, hash, created_at
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
		id                                           int64
		tenantID, resourceID, correlationID, eventID *string
		actor, action, resourceType                  string
		metadataJSON                                 []byte
		prevHash, hash                               []byte
		occurredAt, createdAt                        time.Time
	)

	if err := row.Scan(
		&id, &tenantID, &actor, &action, &resourceType, &resourceID,
		&metadataJSON, &correlationID, &eventID, &occurredAt, &prevHash, &hash, &createdAt,
	); err != nil {
		return nil, err
	}

	var metadata map[string]string
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	// Se reconstituye el valor tal cual está guardado (sin validar contra el
	// catálogo): si una fila fue alterada, VerifyHash debe verla como está.
	return domain.ReconstituteAuditLogEntry(domain.EntrySnapshot{
		ID:            id,
		TenantID:      deref(tenantID),
		Actor:         actor,
		Action:        domain.Action(action),
		ResourceType:  domain.ResourceType(resourceType),
		ResourceID:    deref(resourceID),
		Metadata:      metadata,
		CorrelationID: deref(correlationID),
		EventID:       deref(eventID),
		OccurredAt:    occurredAt.UTC(),
		PrevHash:      prevHash,
		Hash:          hash,
		CreatedAt:     createdAt.UTC(),
	}), nil
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
