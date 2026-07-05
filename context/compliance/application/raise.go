package application

import (
	"context"
	"errors"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// raiseAlert persiste una alerta y publica sus eventos en la misma transacción.
//
// Idempotencia: si la alerta ya existe (unicidad tenant+tipo+subject), el repo
// retorna domain.ErrDuplicateAlert y se trata como no-op (sin publicar) para
// tolerar la re-entrega de eventos de JetStream.
func raiseAlert(
	ctx context.Context,
	tx TxManager,
	repo AlertRepository,
	pub EventPublisher,
	a *domain.Alert,
) error {
	return tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := repo.Save(ctx, a); err != nil {
			if errors.Is(err, domain.ErrDuplicateAlert) {
				return nil // alerta ya existente → idempotente
			}
			return err
		}
		return pub.Publish(ctx, a.PullEvents()...)
	})
}
