package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// pgCheckpointRepository implementa CheckpointRepository sobre
// audit_chain_checkpoints (append-only por trigger, sin RLS).
type pgCheckpointRepository struct {
	pool pgxPool
}

func NewCheckpointRepository(pool pgxPool) *pgCheckpointRepository {
	return &pgCheckpointRepository{pool: pool}
}

func (r *pgCheckpointRepository) Save(ctx context.Context, cp domain.ChainCheckpoint) error {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO audit_chain_checkpoints (entry_id, entry_hash, created_at)
		VALUES ($1, $2, $3)`, cp.EntryID, cp.EntryHash, cp.CreatedAt); err != nil {
		return fmt.Errorf("checkpoint repo: save: %w", err)
	}
	return nil
}

func (r *pgCheckpointRepository) Latest(ctx context.Context) (*domain.ChainCheckpoint, error) {
	var cp domain.ChainCheckpoint
	err := r.pool.QueryRow(ctx, `
		SELECT id, entry_id, entry_hash, created_at
		FROM audit_chain_checkpoints
		ORDER BY id DESC
		LIMIT 1`).Scan(&cp.ID, &cp.EntryID, &cp.EntryHash, &cp.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint repo: latest: %w", err)
	}
	cp.CreatedAt = cp.CreatedAt.UTC()
	return &cp, nil
}

// FirstMismatch busca el primer checkpoint que la cadena ya no respeta: su
// entrada falta (LEFT JOIN sin fila) o tiene otro hash.
func (r *pgCheckpointRepository) FirstMismatch(ctx context.Context) (*domain.CheckpointMismatch, error) {
	var (
		m       domain.CheckpointMismatch
		current []byte
	)
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.entry_id, a.hash
		FROM audit_chain_checkpoints c
		LEFT JOIN audit_log a ON a.id = c.entry_id
		WHERE a.id IS NULL OR a.hash <> c.entry_hash
		ORDER BY c.id
		LIMIT 1`).Scan(&m.CheckpointID, &m.EntryID, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint repo: first mismatch: %w", err)
	}
	m.EntryMissing = current == nil
	return &m, nil
}
