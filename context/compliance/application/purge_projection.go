package application

import (
	"context"
	"log/slog"
	"time"
)

// Retención de la proyección aml_monitored_payments. Las reglas solo miran la
// última ventana (minutos); se conservan unos días para investigaciones y
// para tolerar backlogs del worker. Las alertas (aml_alerts) nunca se purgan:
// su conservación es una exigencia normativa.
const (
	DefaultProjectionRetention = 7 * 24 * time.Hour
	DefaultPurgeInterval       = time.Hour
	purgeBatchSize             = 5_000
)

// ProjectionPurger borra periódicamente los pagos de la proyección más
// antiguos que la retención. Corre en cmd/worker como goroutine.
type ProjectionPurger struct {
	txLog     TransactionLog
	clock     Clock
	retention time.Duration
	interval  time.Duration
	logger    *slog.Logger
}

// NewProjectionPurger crea el purgador. La retención nunca es menor que la
// ventana de las reglas: purgar dentro de la ventana falsearía velocity/volumen.
func NewProjectionPurger(txLog TransactionLog, clock Clock, rules MonitoringRules,
	retention, interval time.Duration, logger *slog.Logger) *ProjectionPurger {
	if retention < rules.VelocityWindow {
		retention = rules.VelocityWindow
	}
	if retention <= 0 {
		retention = DefaultProjectionRetention
	}
	if interval <= 0 {
		interval = DefaultPurgeInterval
	}
	return &ProjectionPurger{txLog: txLog, clock: clock, retention: retention, interval: interval, logger: logger}
}

func (p *ProjectionPurger) Start(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.logger.Info("aml projection purger started", "retention", p.retention.String())

	p.runCycle(ctx) // al arrancar, para no esperar un intervalo completo
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.runCycle(ctx)
		}
	}
}

// runCycle borra en lotes hasta que no queden filas vencidas (lotes chicos:
// no bloquea la tabla ni infla una sola transacción).
func (p *ProjectionPurger) runCycle(ctx context.Context) {
	before := p.clock.Now().Add(-p.retention)
	total := 0
	for ctx.Err() == nil {
		n, err := p.txLog.PurgeBefore(ctx, before, purgeBatchSize)
		if err != nil {
			p.logger.Error("aml projection purger: purge", "error", err)
			break
		}
		total += n
		if n < purgeBatchSize {
			break
		}
	}
	if total > 0 {
		p.logger.Info("aml projection purger: purged", "rows", total, "before", before)
	}
}
