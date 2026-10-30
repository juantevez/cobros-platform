package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// DefaultCheckpointInterval es cada cuánto se guarda la cabeza de la cadena.
const DefaultCheckpointInterval = 15 * time.Minute

// ChainCheckpointer guarda periódicamente la cabeza de la cadena de audit_log
// y la loguea. El log es el ancla fuera de la base: aunque alguien con acceso
// a PostgreSQL reescriba la cadena y los checkpoints, los (entry_id, hash)
// ya emitidos al sistema de logs lo contradicen. Corre en cmd/worker.
type ChainCheckpointer struct {
	entries     AuditLogRepository
	checkpoints CheckpointRepository
	clock       Clock
	interval    time.Duration
	logger      *slog.Logger
}

func NewChainCheckpointer(entries AuditLogRepository, checkpoints CheckpointRepository,
	clock Clock, interval time.Duration, logger *slog.Logger) *ChainCheckpointer {
	if interval <= 0 {
		interval = DefaultCheckpointInterval
	}
	return &ChainCheckpointer{entries: entries, checkpoints: checkpoints, clock: clock, interval: interval, logger: logger}
}

func (c *ChainCheckpointer) Start(ctx context.Context) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.logger.Info("audit chain checkpointer started", "interval", c.interval.String())

	c.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *ChainCheckpointer) runOnce(ctx context.Context) {
	cp, err := c.Checkpoint(ctx)
	if err != nil {
		c.logger.Error("audit chain checkpoint failed", "error", err)
		return
	}
	if cp != nil {
		// Ancla externa: no cambiar el mensaje ni los campos sin actualizar
		// las búsquedas/alertas del sistema de logs.
		c.logger.Info("audit chain checkpoint",
			"entry_id", cp.EntryID, "entry_hash", cp.EntryHashHex())
	}
}

// Checkpoint guarda la cabeza actual si avanzó desde el último checkpoint.
// Retorna nil si no hay nada nuevo que registrar.
func (c *ChainCheckpointer) Checkpoint(ctx context.Context) (*domain.ChainCheckpoint, error) {
	head, err := c.entries.FindLast(ctx)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: find head: %w", err)
	}
	if head == nil {
		return nil, nil // cadena vacía
	}
	latest, err := c.checkpoints.Latest(ctx)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: latest: %w", err)
	}
	if latest != nil && latest.EntryID == head.ID() {
		return nil, nil // la cadena no avanzó
	}
	cp := domain.ChainCheckpoint{EntryID: head.ID(), EntryHash: head.Hash(), CreatedAt: c.clock.Now()}
	if err := c.checkpoints.Save(ctx, cp); err != nil {
		return nil, fmt.Errorf("checkpoint: save: %w", err)
	}
	return &cp, nil
}
