package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

// ── OpenDispute ───────────────────────────────────────────────────────────────

// OpenDisputeUseCase registra una nueva disputa notificada por el banco/PSP.
// Emite DisputeOpenedEvent → Ledger congela fondos en dispute_hold.
type OpenDisputeUseCase struct {
	repo      DisputeRepository
	txManager TxManager
	publisher EventPublisher
}

func NewOpenDisputeUseCase(repo DisputeRepository, txManager TxManager, publisher EventPublisher) *OpenDisputeUseCase {
	return &OpenDisputeUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *OpenDisputeUseCase) Execute(ctx context.Context, cmd OpenDisputeCmd) (OpenDisputeResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	reason, err := domain.ParseDisputeReason(cmd.Reason)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	// Idempotencia: solo una disputa por pago.
	if existing, err := uc.repo.FindByPaymentID(ctx, cmd.PaymentID); err == nil && existing != nil {
		return OpenDisputeResult{}, domain.ErrDuplicateDispute
	}

	if cmd.Deadline.IsZero() {
		cmd.Deadline = time.Now().UTC().Add(7 * 24 * time.Hour) // default 7 días
	}

	id := domain.NewDisputeID()
	dispute, err := domain.NewDispute(
		id, tenantID,
		cmd.PaymentID, cmd.PSPReference,
		cmd.Amount, cmd.Currency,
		reason, cmd.Deadline,
	)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Save(ctx, dispute); err != nil {
			return fmt.Errorf("save dispute: %w", err)
		}
		return uc.publisher.Publish(ctx, dispute.PullEvents()...)
	}); err != nil {
		return OpenDisputeResult{}, err
	}

	return OpenDisputeResult{DisputeID: id.String()}, nil
}

// ── ContestDispute ────────────────────────────────────────────────────────────

// ContestDisputeUseCase envía evidencia del comercio para contestar la disputa.
type ContestDisputeUseCase struct {
	repo      DisputeRepository
	txManager TxManager
	clock     Clock
}

func NewContestDisputeUseCase(repo DisputeRepository, txManager TxManager, clock Clock) *ContestDisputeUseCase {
	return &ContestDisputeUseCase{repo: repo, txManager: txManager, clock: clock}
}

func (uc *ContestDisputeUseCase) Execute(ctx context.Context, cmd ContestDisputeCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	id, err := domain.ParseDisputeID(cmd.DisputeID)
	if err != nil {
		return err
	}

	dispute, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if dispute.TenantID() != tenantID {
		return domain.ErrDisputeNotFound
	}

	evidence := make([]domain.Evidence, len(cmd.Evidence))
	for i, e := range cmd.Evidence {
		evidence[i] = domain.NewEvidence(domain.NewEvidenceID(), e.EvidenceType, e.Reference, e.Description)
	}

	if err := dispute.Contest(evidence, cmd.Note, uc.clock.Now()); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		return uc.repo.Update(ctx, dispute)
	})
}

// ── AcceptDispute ─────────────────────────────────────────────────────────────

// AcceptDisputeUseCase acepta la disputa voluntariamente.
// Emite DisputeResolvedEvent → Ledger libera los fondos del hold hacia el banco.
type AcceptDisputeUseCase struct {
	repo      DisputeRepository
	txManager TxManager
	publisher EventPublisher
}

func NewAcceptDisputeUseCase(repo DisputeRepository, txManager TxManager, publisher EventPublisher) *AcceptDisputeUseCase {
	return &AcceptDisputeUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *AcceptDisputeUseCase) Execute(ctx context.Context, cmd AcceptDisputeCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	id, err := domain.ParseDisputeID(cmd.DisputeID)
	if err != nil {
		return err
	}

	dispute, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if dispute.TenantID() != tenantID {
		return domain.ErrDisputeNotFound
	}

	if err := dispute.Accept(cmd.Note); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, dispute); err != nil {
			return err
		}
		return uc.publisher.Publish(ctx, dispute.PullEvents()...)
	})
}

// ── ResolveDispute ────────────────────────────────────────────────────────────

// ResolveDisputeUseCase registra el resultado final del banco.
// Emite DisputeResolvedEvent → Ledger libera (won) o debita (lost) los fondos.
type ResolveDisputeUseCase struct {
	repo      DisputeRepository
	txManager TxManager
	publisher EventPublisher
}

func NewResolveDisputeUseCase(repo DisputeRepository, txManager TxManager, publisher EventPublisher) *ResolveDisputeUseCase {
	return &ResolveDisputeUseCase{repo: repo, txManager: txManager, publisher: publisher}
}

func (uc *ResolveDisputeUseCase) Execute(ctx context.Context, cmd ResolveDisputeCmd) error {
	id, err := domain.ParseDisputeID(cmd.DisputeID)
	if err != nil {
		return err
	}

	outcome, err := domain.ParseResolutionOutcome(cmd.Outcome)
	if err != nil {
		return err
	}

	dispute, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if err := dispute.Resolve(outcome, cmd.Note); err != nil {
		return err
	}

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, dispute); err != nil {
			return err
		}
		return uc.publisher.Publish(ctx, dispute.PullEvents()...)
	})
}

// ── GetDispute / ListDisputes ─────────────────────────────────────────────────

type GetDisputeUseCase struct {
	repo  DisputeRepository
	clock Clock
}

func NewGetDisputeUseCase(repo DisputeRepository, clock Clock) *GetDisputeUseCase {
	return &GetDisputeUseCase{repo: repo, clock: clock}
}

func (uc *GetDisputeUseCase) Execute(ctx context.Context, q GetDisputeQuery) (DisputeView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return DisputeView{}, err
	}
	id, err := domain.ParseDisputeID(q.DisputeID)
	if err != nil {
		return DisputeView{}, err
	}

	d, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return DisputeView{}, err
	}
	if d.TenantID() != tenantID {
		return DisputeView{}, domain.ErrDisputeNotFound
	}
	return toView(d, uc.clock.Now()), nil
}

type ListDisputesUseCase struct {
	repo  DisputeRepository
	clock Clock
}

func NewListDisputesUseCase(repo DisputeRepository, clock Clock) *ListDisputesUseCase {
	return &ListDisputesUseCase{repo: repo, clock: clock}
}

func (uc *ListDisputesUseCase) Execute(ctx context.Context, q ListDisputesQuery) ([]DisputeView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	disputes, err := uc.repo.ListByTenant(ctx, tenantID, q.StatusFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("list disputes: %w", err)
	}

	now := uc.clock.Now()
	views := make([]DisputeView, len(disputes))
	for i, d := range disputes {
		views[i] = toView(d, now)
	}
	return views, nil
}

// ── ExpiryPoller ─────────────────────────────────────────────────────────────

// ExpiryPoller marca como expired las disputes abiertas cuyo deadline venció.
// Corre en cmd/worker como goroutine independiente cada hora.
type ExpiryPoller struct {
	repo      DisputeRepository
	txManager TxManager
	publisher EventPublisher
	logger    *slog.Logger
	clock     Clock
}

func NewExpiryPoller(
	repo DisputeRepository,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
	logger *slog.Logger,
) *ExpiryPoller {
	return &ExpiryPoller{repo: repo, txManager: txManager, publisher: publisher,
		clock: clock, logger: logger}
}

func (p *ExpiryPoller) Start(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	p.logger.Info("dispute expiry poller started")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.runCycle(ctx)
		}
	}
}

func (p *ExpiryPoller) runCycle(ctx context.Context) {
	now := p.clock.Now()
	overdue, err := p.repo.ListOverdue(ctx, now, 100)
	if err != nil {
		p.logger.Error("dispute expiry poller: list overdue", "error", err)
		return
	}
	for _, d := range overdue {
		if err := d.Expire(); err != nil {
			continue
		}
		if err := p.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := p.repo.Update(ctx, d); err != nil {
				return err
			}
			return p.publisher.Publish(ctx, d.PullEvents()...)
		}); err != nil {
			p.logger.Error("dispute expiry poller: expire dispute",
				"dispute_id", d.ID(), "error", err)
		}
	}
	if len(overdue) > 0 {
		p.logger.Info("dispute expiry poller: expired", "count", len(overdue))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toView(d *domain.Dispute, now time.Time) DisputeView {
	ev := make([]EvidenceView, len(d.Evidence()))
	for i, e := range d.Evidence() {
		ev[i] = EvidenceView{
			ID:           e.ID().String(),
			EvidenceType: e.EvidenceType(),
			Reference:    e.Reference(),
			Description:  e.Description(),
			SubmittedAt:  e.SubmittedAt().Format(time.RFC3339),
		}
	}

	v := DisputeView{
		ID:           d.ID().String(),
		TenantID:     d.TenantID().String(),
		PaymentID:    d.PaymentID(),
		PSPReference: d.PSPReference(),
		Amount:       d.Amount(),
		Currency:     d.Currency(),
		Reason:       d.Reason().String(),
		Status:       d.Status().String(),
		Evidence:     ev,
		ResponseNote: d.ResponseNote(),
		ResolvedNote: d.ResolvedNote(),
		Deadline:     d.Deadline().Format(time.RFC3339),
		OpenedAt:     d.OpenedAt().Format(time.RFC3339),
		IsOverdue:    d.IsOverdue(now),
	}
	if r := d.RespondedAt(); r != nil {
		s := r.Format(time.RFC3339)
		v.RespondedAt = &s
	}
	if r := d.ResolvedAt(); r != nil {
		s := r.Format(time.RFC3339)
		v.ResolvedAt = &s
	}
	return v
}
