package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// ErrDuplicatePaymentKey es el centinela que el repositorio retorna cuando el
// INSERT falla contra la constraint UNIQUE(payment_id). El caso de uso lo
// traduce a domain.ErrDuplicateDispute (HTTP 409) en lugar de un 500.
var ErrDuplicatePaymentKey = errors.New("duplicate payment key")

// ── OpenDispute ───────────────────────────────────────────────────────────────

// OpenDisputeUseCase registra una nueva disputa notificada por el banco/PSP.
// Emite DisputeOpenedEvent → Ledger congela fondos en dispute_hold.
type OpenDisputeUseCase struct {
	repo      DisputeRepository
	payments  PaymentReader
	txManager TxManager
	publisher EventPublisher
	clock     Clock
}

// defaultResponseWindow es el plazo que se aplica cuando el operador no indica
// un deadline explícito (7 días corridos desde la apertura).
const defaultResponseWindow = 7 * 24 * time.Hour

func NewOpenDisputeUseCase(
	repo DisputeRepository,
	payments PaymentReader,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *OpenDisputeUseCase {
	return &OpenDisputeUseCase{repo: repo, payments: payments, txManager: txManager,
		publisher: publisher, clock: clock}
}

func (uc *OpenDisputeUseCase) Execute(ctx context.Context, cmd OpenDisputeCmd) (OpenDisputeResult, error) {
	reason, err := domain.ParseDisputeReason(cmd.Reason)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	// now se resuelve con el reloj inyectado: en producción es time.Now(), pero
	// los tests lo fijan para poder construir disputas con deadlines relativos a
	// "ahora" (un deadline fijo en el tiempo eventualmente vence, y un caso de uso
	// que dependa del reloj real no es determinístico).
	now := uc.clock.Now()

	// Idempotencia: solo una disputa por pago. Un error de infraestructura NO
	// puede tratarse como "sin duplicado": se propaga para no abrir dos disputas
	// sobre el mismo payment_id.
	existing, err := uc.repo.FindByPaymentID(ctx, cmd.PaymentID)
	switch {
	case err == nil && existing != nil:
		return OpenDisputeResult{}, domain.ErrDuplicateDispute
	case err != nil && !errors.Is(err, domain.ErrDisputeNotFound):
		return OpenDisputeResult{}, fmt.Errorf("check duplicate dispute: %w", err)
	}

	// Validaciones cross-context del pago (invariantes en el dominio):
	// existencia, estado captured, titularidad, monto y moneda.
	payment, err := uc.payments.FindPayment(ctx, cmd.PaymentID)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	if cmd.Deadline.IsZero() {
		cmd.Deadline = now.Add(defaultResponseWindow)
	}

	id := domain.NewDisputeID()
	dispute, err := domain.NewDispute(
		id,
		payment, cmd.PSPReference,
		cmd.Amount, cmd.Currency,
		reason, cmd.Deadline, now,
	)
	if err != nil {
		return OpenDisputeResult{}, err
	}

	// La disputa se escribe con el tenant del pago (RLS), no con el del
	// operador que la abre.
	ctx = pkgpostgres.WithTenantID(ctx, dispute.TenantID().String())
	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Save(ctx, dispute); err != nil {
			return fmt.Errorf("save dispute: %w", err)
		}
		return uc.publisher.Publish(ctx, dispute.PullEvents()...)
	}); err != nil {
		// Si la carrera la ganó la constraint UNIQUE(payment_id), respondemos
		// 409 (ErrDuplicateDispute) y no un 500 genérico.
		if errors.Is(err, ErrDuplicatePaymentKey) {
			return OpenDisputeResult{}, domain.ErrDuplicateDispute
		}
		return OpenDisputeResult{}, err
	}

	return OpenDisputeResult{DisputeID: dispute.ID().String()}, nil
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

	evidence := make([]domain.Evidence, 0, len(cmd.Evidence))
	for _, e := range cmd.Evidence {
		ev, err := domain.NewEvidence(domain.NewEvidenceID(), e.EvidenceType, e.Reference, e.Description, uc.clock.Now())
		if err != nil {
			return err
		}
		evidence = append(evidence, ev)
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
	clock     Clock
}

func NewAcceptDisputeUseCase(repo DisputeRepository, txManager TxManager, publisher EventPublisher, clock Clock) *AcceptDisputeUseCase {
	return &AcceptDisputeUseCase{repo: repo, txManager: txManager, publisher: publisher, clock: clock}
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

	if err := dispute.Accept(cmd.Note, uc.clock.Now()); err != nil {
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
	clock     Clock
}

func NewResolveDisputeUseCase(repo DisputeRepository, txManager TxManager, publisher EventPublisher, clock Clock) *ResolveDisputeUseCase {
	return &ResolveDisputeUseCase{repo: repo, txManager: txManager, publisher: publisher, clock: clock}
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

	if err := dispute.Resolve(outcome, cmd.Note, uc.clock.Now()); err != nil {
		return err
	}

	// El operador lee la disputa con la política de operador (solo lectura);
	// la escritura corre con el tenant de la disputa (RLS).
	ctx = pkgpostgres.WithTenantID(ctx, dispute.TenantID().String())
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
// Corre en cmd/worker como goroutine independiente: un ciclo al arrancar y
// después cada expiryInterval.
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

const (
	// expiryInterval acota la demora entre el vencimiento y el cierre (antes
	// era de hasta una hora, y el primer ciclo recién corría a la hora).
	expiryInterval = 5 * time.Minute
	expiryBatch    = 100
	// maxExpiryBatches acota un ciclo: con más vencidas, siguen en el próximo.
	maxExpiryBatches = 10
)

func (p *ExpiryPoller) Start(ctx context.Context) error {
	ticker := time.NewTicker(expiryInterval)
	defer ticker.Stop()
	p.logger.Info("dispute expiry poller started", "interval", expiryInterval)

	if ctx.Err() == nil {
		p.runCycle(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.runCycle(ctx)
		}
	}
}

// runCycle vence las disputas abiertas con deadline pasado, de a lotes de
// expiryBatch. Corta con un lote incompleto (no quedan más) o sin avances:
// las que fallan siguen vencidas y pedirlas otra vez daría siempre las mismas.
func (p *ExpiryPoller) runCycle(ctx context.Context) {
	now := p.clock.Now()
	total := 0
	for i := 0; i < maxExpiryBatches; i++ {
		overdue, err := p.repo.ListOverdue(ctx, now, expiryBatch)
		if err != nil {
			p.logger.Error("dispute expiry poller: list overdue", "error", err)
			break
		}
		expired := p.expireBatch(ctx, overdue, now)
		total += expired
		if len(overdue) < expiryBatch || expired == 0 {
			break
		}
	}
	if total > 0 {
		p.logger.Info("dispute expiry poller: expired", "count", total)
	}
}

// expireBatch vence cada disputa del lote y devuelve cuántas cerró.
func (p *ExpiryPoller) expireBatch(ctx context.Context, overdue []*domain.Dispute, now time.Time) int {
	expired := 0
	for _, d := range overdue {
		if err := d.Expire(now); err != nil {
			continue
		}
		// ListOverdue es cross-tenant; cada vencimiento se escribe con el
		// tenant de su disputa (RLS).
		tctx := pkgpostgres.WithTenantID(ctx, d.TenantID().String())
		if err := p.txManager.RunInTx(tctx, func(ctx context.Context) error {
			if err := p.repo.Update(ctx, d); err != nil {
				return err
			}
			return p.publisher.Publish(ctx, d.PullEvents()...)
		}); err != nil {
			if errors.Is(err, domain.ErrOptimisticLock) {
				// El comercio la contestó o aceptó mientras tanto: no vence.
				p.logger.Info("dispute expiry poller: dispute changed concurrently, skipped",
					"dispute_id", d.ID())
				continue
			}
			p.logger.Error("dispute expiry poller: expire dispute",
				"dispute_id", d.ID(), "error", err)
			continue
		}
		expired++
	}
	return expired
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
		Version:      d.Version(),
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
