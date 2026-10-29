package application

import (
	"context"
	"errors"
	"time"

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
	return raiseWith(ctx, tx, pub, a, func(ctx context.Context) error { return repo.Save(ctx, a) })
}

// raiseWindowAlert es raiseAlert para reglas de ventana: además de la
// unicidad por subject aplica el cooldown (SaveUnlessRecent). Una alerta
// reciente del mismo tipo también es un no-op.
func raiseWindowAlert(
	ctx context.Context,
	tx TxManager,
	repo AlertRepository,
	pub EventPublisher,
	a *domain.Alert,
	subjectPrefix string,
	since time.Time,
) error {
	return raiseWith(ctx, tx, pub, a, func(ctx context.Context) error {
		return repo.SaveUnlessRecent(ctx, a, subjectPrefix, since)
	})
}

func raiseWith(ctx context.Context, tx TxManager, pub EventPublisher, a *domain.Alert, save func(context.Context) error) error {
	return tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := save(ctx); err != nil {
			if errors.Is(err, domain.ErrDuplicateAlert) {
				return nil // ya existente o en cooldown → idempotente
			}
			return err
		}
		return pub.Publish(ctx, a.PullEvents()...)
	})
}
