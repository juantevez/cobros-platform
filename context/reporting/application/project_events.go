package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

// Clock abstrae el reloj para poder testear la proyección sin tiempo real.
type Clock interface {
	Now() time.Time
}

// ProjectEventsUseCase traduce eventos de dominio de otros contextos en hechos
// del read-model. Es el corazón del lado de escritura de este contexto CQRS.
//
// No emite eventos ni corre transacciones de negocio: solo persiste hechos
// idempotentes que luego se agregan al consultar.
type ProjectEventsUseCase struct {
	writer   ProjectionWriter
	accounts AccountTypeReader
	clock    Clock
}

func NewProjectEventsUseCase(writer ProjectionWriter, accounts AccountTypeReader, clock Clock) *ProjectEventsUseCase {
	return &ProjectEventsUseCase{writer: writer, accounts: accounts, clock: clock}
}

// PaymentCapturedCmd son los campos de payment.captured.v1 relevantes al reporting.
type PaymentCapturedCmd struct {
	PaymentID     string
	TenantID      string
	Currency      string
	Amount        int64
	PlatformFee   int64
	PSPFee        int64
	PaymentMethod string
	// OccurredAt es el instante del evento de origen. Cero = desconocido
	// (se usa el reloj, con el riesgo de fechar mal re-entregas o replays).
	OccurredAt time.Time
}

// ProjectPaymentCaptured proyecta un pago capturado como un PaymentFact.
func (uc *ProjectEventsUseCase) ProjectPaymentCaptured(ctx context.Context, cmd PaymentCapturedCmd) error {
	if cmd.PaymentID == "" || cmd.TenantID == "" {
		return domain.ErrInvalidFact
	}
	return uc.writer.SavePaymentFact(ctx, domain.PaymentFact{
		PaymentID:     cmd.PaymentID,
		TenantID:      cmd.TenantID,
		Currency:      cmd.Currency,
		Amount:        cmd.Amount,
		PlatformFee:   cmd.PlatformFee,
		PSPFee:        cmd.PSPFee,
		PaymentMethod: cmd.PaymentMethod,
		CapturedAt:    uc.factTime(cmd.OccurredAt),
	})
}

// PostingCmd es un posting individual de un asiento del ledger.
// El tipo de cuenta no viaja en el evento; se resuelve vía AccountTypeReader.
type PostingCmd struct {
	AccountID string
	Direction string
	Amount    int64
	Currency  string
}

// EntryPostedCmd son los campos de ledger.entry.posted.v1 relevantes al reporting.
type EntryPostedCmd struct {
	EntryID    string
	TenantID   string
	Postings   []PostingCmd
	OccurredAt time.Time // ver PaymentCapturedCmd.OccurredAt
}

// ProjectEntryPosted proyecta cada posting de un asiento como un LedgerMovement.
// La idempotencia la garantiza la clave (entry_id, account_id, direction) en el writer.
func (uc *ProjectEventsUseCase) ProjectEntryPosted(ctx context.Context, cmd EntryPostedCmd) error {
	if cmd.EntryID == "" || cmd.TenantID == "" {
		return domain.ErrInvalidFact
	}
	postedAt := uc.factTime(cmd.OccurredAt)
	for _, p := range cmd.Postings {
		if p.AccountID == "" {
			continue // posting sin cuenta: skip defensivo
		}
		// Denormalizamos el tipo de cuenta para agregar balances sin JOINs.
		// Si la cuenta no se encuentra, proyectamos con tipo vacío (mejor
		// que perder el movimiento).
		accountType, err := uc.accounts.AccountType(ctx, cmd.TenantID, p.AccountID)
		if err != nil {
			return err
		}
		if err := uc.writer.SaveLedgerMovement(ctx, domain.LedgerMovement{
			EntryID:     cmd.EntryID,
			AccountID:   p.AccountID,
			Direction:   p.Direction,
			TenantID:    cmd.TenantID,
			AccountType: accountType,
			Currency:    p.Currency,
			Amount:      p.Amount,
			PostedAt:    postedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PaymentRefundedCmd son los campos de payment.refunded.v1 relevantes al reporting.
type PaymentRefundedCmd struct {
	PaymentID  string
	TenantID   string
	Currency   string
	Amount     int64
	OccurredAt time.Time // ver PaymentCapturedCmd.OccurredAt
}

// ProjectPaymentRefunded proyecta un reembolso como ajuste. El reembolso es
// total y único por pago, así que su clave es el payment_id.
func (uc *ProjectEventsUseCase) ProjectPaymentRefunded(ctx context.Context, cmd PaymentRefundedCmd) error {
	if cmd.PaymentID == "" || cmd.TenantID == "" {
		return domain.ErrInvalidFact
	}
	return uc.writer.SavePaymentAdjustment(ctx, domain.PaymentAdjustment{
		Kind:       domain.AdjustmentRefund,
		SourceID:   cmd.PaymentID,
		PaymentID:  cmd.PaymentID,
		TenantID:   cmd.TenantID,
		Currency:   cmd.Currency,
		Amount:     cmd.Amount,
		OccurredAt: uc.factTime(cmd.OccurredAt),
	})
}

// DisputeResolvedCmd son los campos de dispute.resolved.v1 relevantes al reporting.
type DisputeResolvedCmd struct {
	DisputeID  string
	PaymentID  string
	TenantID   string
	Currency   string
	Amount     int64
	Outcome    string // "won" | "lost" | "accepted" | "expired"
	OccurredAt time.Time
}

// chargebackOutcomes son los resultados de disputa en que el comercio pierde
// el monto disputado.
var chargebackOutcomes = map[string]bool{"lost": true, "accepted": true, "expired": true}

// ProjectDisputeResolved proyecta un contracargo cuando la disputa se perdió,
// se aceptó o venció. Una disputa ganada no mueve dinero: no hay hecho.
func (uc *ProjectEventsUseCase) ProjectDisputeResolved(ctx context.Context, cmd DisputeResolvedCmd) error {
	if cmd.DisputeID == "" || cmd.PaymentID == "" || cmd.TenantID == "" {
		return domain.ErrInvalidFact
	}
	if cmd.Outcome == "won" {
		return nil
	}
	if !chargebackOutcomes[cmd.Outcome] {
		return fmt.Errorf("%w: unknown dispute outcome %q", domain.ErrInvalidFact, cmd.Outcome)
	}
	return uc.writer.SavePaymentAdjustment(ctx, domain.PaymentAdjustment{
		Kind:       domain.AdjustmentChargeback,
		SourceID:   cmd.DisputeID,
		PaymentID:  cmd.PaymentID,
		TenantID:   cmd.TenantID,
		Currency:   cmd.Currency,
		Amount:     cmd.Amount,
		OccurredAt: uc.factTime(cmd.OccurredAt),
	})
}

// factTime fecha un hecho con el instante del evento de origen, para que las
// series agreguen por cuándo ocurrió y no por cuándo se proyectó (re-entregas
// tardías o replays del read-model). Sin ese dato, cae al reloj.
func (uc *ProjectEventsUseCase) factTime(occurredAt time.Time) time.Time {
	if occurredAt.IsZero() {
		return uc.clock.Now()
	}
	return occurredAt.UTC()
}
