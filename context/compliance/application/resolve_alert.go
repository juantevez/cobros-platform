package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// La revisión de alertas la hace el equipo de cumplimiento de la plataforma
// (rol compliance_officer), sobre alertas de cualquier comercio: el comercio
// nunca dispone sus propias alertas. Los tres casos de uso comparten el mismo
// patrón transaccional (reviewAlert).

// ResolveAlertUseCase registra la disposición del primer analista. En alertas
// four-eyes la deja como propuesta (pending_second_approval).
type ResolveAlertUseCase struct{ reviewer }

func NewResolveAlertUseCase(repo AlertRepository, txManager TxManager, publisher EventPublisher, clock Clock) *ResolveAlertUseCase {
	return &ResolveAlertUseCase{reviewer{repo: repo, txManager: txManager, publisher: publisher, clock: clock}}
}

func (uc *ResolveAlertUseCase) Execute(ctx context.Context, cmd ResolveAlertCmd) error {
	status, err := domain.ParseDisposition(cmd.Disposition)
	if err != nil {
		return err
	}
	return uc.review(ctx, cmd.AlertID, func(a *domain.Alert) error {
		return a.Resolve(status, cmd.Note, cmd.Analyst, uc.clock.Now())
	})
}

// ApproveAlertProposalUseCase cierra una alerta four-eyes con la disposición
// propuesta. El analista debe ser distinto del que propuso.
type ApproveAlertProposalUseCase struct{ reviewer }

func NewApproveAlertProposalUseCase(repo AlertRepository, txManager TxManager, publisher EventPublisher, clock Clock) *ApproveAlertProposalUseCase {
	return &ApproveAlertProposalUseCase{reviewer{repo: repo, txManager: txManager, publisher: publisher, clock: clock}}
}

func (uc *ApproveAlertProposalUseCase) Execute(ctx context.Context, cmd ReviewProposalCmd) error {
	return uc.review(ctx, cmd.AlertID, func(a *domain.Alert) error {
		return a.ApproveProposal(cmd.Note, cmd.Analyst, uc.clock.Now())
	})
}

// RejectAlertProposalUseCase descarta la propuesta y devuelve la alerta a open.
type RejectAlertProposalUseCase struct{ reviewer }

func NewRejectAlertProposalUseCase(repo AlertRepository, txManager TxManager, publisher EventPublisher, clock Clock) *RejectAlertProposalUseCase {
	return &RejectAlertProposalUseCase{reviewer{repo: repo, txManager: txManager, publisher: publisher, clock: clock}}
}

func (uc *RejectAlertProposalUseCase) Execute(ctx context.Context, cmd ReviewProposalCmd) error {
	return uc.review(ctx, cmd.AlertID, func(a *domain.Alert) error {
		return a.RejectProposal(cmd.Note, cmd.Analyst, uc.clock.Now())
	})
}

type reviewer struct {
	repo      AlertRepository
	txManager TxManager
	publisher EventPublisher
	clock     Clock
}

// review carga la alerta, aplica la transición y la persiste con UPDATE
// condicional sobre el estado previo, todo en la misma tx. Si dos analistas
// actúan a la vez, el segundo UPDATE no afecta filas (ErrAlertNotOpen), la tx
// hace rollback y no se publican eventos duplicados.
func (r reviewer) review(ctx context.Context, rawID string, transition func(*domain.Alert) error) error {
	alertID, err := domain.ParseAlertID(rawID)
	if err != nil {
		return err
	}
	return r.txManager.RunInTx(ctx, func(ctx context.Context) error {
		alert, err := r.repo.FindByID(ctx, alertID)
		if err != nil {
			return err
		}
		from := alert.Status()
		if err := transition(alert); err != nil {
			return err
		}
		if err := r.repo.Update(ctx, alert, from); err != nil {
			return err
		}
		return r.publisher.Publish(ctx, alert.PullEvents()...)
	})
}
