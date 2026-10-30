package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// lifecycle agrupa lo necesario para tomar, cerrar y fallar un run. Lo
// comparten los casos de uso que procesan runs.
type lifecycle struct {
	runs  RunRepository
	discs DiscrepancyRepository
	tx    TxManager
	pub   EventPublisher
	clock Clock
}

// claim verifica que la operación corresponda al tipo del run y lo pasa a
// running (o retoma uno abandonado) con un UPDATE condicionado a la versión
// leída: de dos procesos concurrentes sobre el mismo run, solo uno lo toma
// (el otro recibe ErrRunAlreadyRunning).
func (l lifecycle) claim(ctx context.Context, run *domain.ReconciliationRun, want domain.ReconciliationType) error {
	if run.Type() != want {
		return fmt.Errorf("%w: run %s is %q, operation requires %q", domain.ErrRunTypeMismatch, run.ID(), run.Type(), want)
	}
	if err := run.Start(l.clock.Now()); err != nil {
		return err
	}
	if err := l.runs.Update(ctx, run); err != nil {
		return fmt.Errorf("claim run: %w", err)
	}
	return nil
}

// complete guarda las discrepancias, cierra el run y publica el evento en una
// sola transacción. Si falla, no queda nada a medias y el run pasa a failed.
func (l lifecycle) complete(
	ctx context.Context,
	run *domain.ReconciliationRun,
	discrepancies []*domain.Discrepancy,
	total, matched int,
) error {
	before := *run
	err := l.tx.RunInTx(ctx, func(ctx context.Context) error {
		if len(discrepancies) > 0 {
			if err := l.discs.SaveAll(ctx, discrepancies); err != nil {
				return fmt.Errorf("save discrepancies: %w", err)
			}
		}
		if err := run.Complete(total, matched, len(discrepancies), l.clock.Now()); err != nil {
			return err
		}
		if err := l.runs.Update(ctx, run); err != nil {
			return fmt.Errorf("update run completed: %w", err)
		}
		return l.pub.Publish(ctx, run.PullEvents()...)
	})
	if err != nil {
		*run = before // la tx no confirmó: en la base el run sigue running (y en esa versión)
		return l.fail(ctx, run, err)
	}
	return nil
}

// fail marca el run como fallido y publica reconciliation.run.failed.v1 en
// una transacción, y devuelve la causa. Si tampoco se puede guardar el fallo,
// el run queda running y el error lo informa.
func (l lifecycle) fail(ctx context.Context, run *domain.ReconciliationRun, cause error) error {
	err := l.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := run.Fail(cause.Error(), l.clock.Now()); err != nil {
			return err
		}
		if err := l.runs.Update(ctx, run); err != nil {
			return err
		}
		return l.pub.Publish(ctx, run.PullEvents()...)
	})
	if err != nil {
		return fmt.Errorf("%w (mark run failed: %v)", cause, err)
	}
	return cause
}
