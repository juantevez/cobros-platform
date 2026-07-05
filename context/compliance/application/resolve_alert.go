package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// ResolveAlertUseCase dispone una alerta (revisión manual del analista).
type ResolveAlertUseCase struct {
	repo      AlertRepository
	txManager TxManager
	publisher EventPublisher
	clock     Clock
}

func NewResolveAlertUseCase(repo AlertRepository, txManager TxManager, publisher EventPublisher, clock Clock) *ResolveAlertUseCase {
	return &ResolveAlertUseCase{repo: repo, txManager: txManager, publisher: publisher, clock: clock}
}

func (uc *ResolveAlertUseCase) Execute(ctx context.Context, cmd ResolveAlertCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	alertID, err := domain.ParseAlertID(cmd.AlertID)
	if err != nil {
		return err
	}
	status, err := domain.ParseDisposition(cmd.Disposition)
	if err != nil {
		return err
	}

	alert, err := uc.repo.FindByID(ctx, alertID)
	if err != nil {
		return err
	}
	if alert.TenantID() != tenantID {
		return domain.ErrAlertNotFound
	}

	if err := alert.Resolve(status, cmd.Note, uc.clock.Now()); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, alert); err != nil {
			return err
		}
		return uc.publisher.Publish(ctx, alert.PullEvents()...)
	})
}
