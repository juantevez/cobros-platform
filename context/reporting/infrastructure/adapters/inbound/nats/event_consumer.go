package nats

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/application"
	"github.com/juantevez/cobros-platform/context/reporting/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// EventConsumer proyecta eventos de otros contextos en el read-model de reporting.
//
// Consume dos fuentes:
//   - PAYMENT (payment.captured.v1)  → volumen y revenue
//   - LEDGER  (ledger.entry.posted.v1) → balances por comercio
type EventConsumer struct {
	consumer eventbus.Consumer
	project  *application.ProjectEventsUseCase
	logger   *slog.Logger
}

func NewEventConsumer(
	consumer eventbus.Consumer,
	project *application.ProjectEventsUseCase,
	logger *slog.Logger,
) *EventConsumer {
	return &EventConsumer{consumer: consumer, project: project, logger: logger}
}

func (c *EventConsumer) StartPaymentConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "PAYMENT", Name: "reporting-payment-consumer",
		FilterSubject: "payment.captured.>", MaxDeliver: 5,
	}, c.handle)
}

func (c *EventConsumer) StartLedgerConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "LEDGER", Name: "reporting-ledger-consumer",
		FilterSubject: "ledger.entry.posted.>", MaxDeliver: 5,
	}, c.handle)
}

// paymentCaptured refleja el payload de payment.captured.v1.
type paymentCaptured struct {
	PaymentID     string `json:"payment_id"`
	TenantID      string `json:"tenant_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	PlatformFee   int64  `json:"platform_fee"`
	PSPFee        int64  `json:"psp_fee"`
	PaymentMethod string `json:"payment_method"`
}

// entryPosted refleja el payload de ledger.entry.posted.v1.
type entryPosted struct {
	EntryID  string `json:"entry_id"`
	TenantID string `json:"tenant_id"`
	Postings []struct {
		AccountID string `json:"account_id"`
		Direction string `json:"direction"`
		Amount    int64  `json:"amount"`
		Currency  string `json:"currency"`
	} `json:"postings"`
}

func (c *EventConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	switch msg.Subject {
	case "payment.captured.v1":
		return c.handlePaymentCaptured(ctx, msg)
	case "ledger.entry.posted.v1":
		return c.handleEntryPosted(ctx, msg)
	default:
		return nil // evento no relevante para reporting → ack silencioso
	}
}

func (c *EventConsumer) handlePaymentCaptured(ctx context.Context, msg *eventbus.Message) error {
	var p paymentCaptured
	if err := json.Unmarshal(msg.Payload, &p); err != nil || p.TenantID == "" {
		return nil // payload malformado o sin tenant → ack (no reintentar)
	}
	if err := c.project.ProjectPaymentCaptured(ctx, application.PaymentCapturedCmd{
		PaymentID:     p.PaymentID,
		TenantID:      p.TenantID,
		Currency:      p.Currency,
		Amount:        p.Amount,
		PlatformFee:   p.PlatformFee,
		PSPFee:        p.PSPFee,
		PaymentMethod: p.PaymentMethod,
		OccurredAt:    occurredAt(msg),
	}); err != nil {
		return c.projectionFailed(err, "payment_id", p.PaymentID)
	}
	return nil
}

func (c *EventConsumer) handleEntryPosted(ctx context.Context, msg *eventbus.Message) error {
	var e entryPosted
	if err := json.Unmarshal(msg.Payload, &e); err != nil || e.TenantID == "" {
		return nil
	}
	postings := make([]application.PostingCmd, 0, len(e.Postings))
	for _, p := range e.Postings {
		postings = append(postings, application.PostingCmd{
			AccountID: p.AccountID,
			Direction: p.Direction,
			Amount:    p.Amount,
			Currency:  p.Currency,
		})
	}
	if err := c.project.ProjectEntryPosted(ctx, application.EntryPostedCmd{
		EntryID:    e.EntryID,
		TenantID:   e.TenantID,
		Postings:   postings,
		OccurredAt: occurredAt(msg),
	}); err != nil {
		return c.projectionFailed(err, "entry_id", e.EntryID)
	}
	return nil
}

// projectionFailed decide entre ack y nak ante un error de proyección.
// ErrInvalidFact es permanente (al evento le falta un ID): reintentarlo solo
// agota MaxDeliver y lo manda a la DLQ, así que se descarta igual que un
// payload malformado. Cualquier otro error es transitorio → nak.
func (c *EventConsumer) projectionFailed(err error, idKey, id string) error {
	if errors.Is(err, domain.ErrInvalidFact) {
		c.logger.Warn("reporting: discarding invalid event", idKey, id, "error", err)
		return nil
	}
	c.logger.Error("reporting: projection failed", idKey, id, "error", err)
	return err // nak → reintento con backoff
}

// occurredAt lee el instante del evento del header que agrega el outbox relay.
// Si falta o es inválido retorna cero y el caso de uso cae al reloj.
func occurredAt(msg *eventbus.Message) time.Time {
	t, err := time.Parse(time.RFC3339Nano, msg.Headers[eventbus.HeaderOccurredAt])
	if err != nil {
		return time.Time{}
	}
	return t
}
