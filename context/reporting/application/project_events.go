package application

import (
	"context"
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
		CapturedAt:    uc.clock.Now(),
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
	EntryID  string
	TenantID string
	Postings []PostingCmd
}

// ProjectEntryPosted proyecta cada posting de un asiento como un LedgerMovement.
// La idempotencia la garantiza la clave (entry_id, account_id, direction) en el writer.
func (uc *ProjectEventsUseCase) ProjectEntryPosted(ctx context.Context, cmd EntryPostedCmd) error {
	if cmd.EntryID == "" || cmd.TenantID == "" {
		return domain.ErrInvalidFact
	}
	now := uc.clock.Now()
	for _, p := range cmd.Postings {
		if p.AccountID == "" {
			continue // posting sin cuenta: skip defensivo
		}
		// Denormalizamos el tipo de cuenta para agregar balances sin JOINs.
		// Si la cuenta no se encuentra, proyectamos con tipo vacío (mejor
		// que perder el movimiento).
		accountType, err := uc.accounts.AccountType(ctx, p.AccountID)
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
			PostedAt:    now,
		}); err != nil {
			return err
		}
	}
	return nil
}
