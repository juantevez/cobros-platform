// Package nats contiene el consumer de eventos para el contexto Webhook.
// Suscribe a los streams de todos los contextos que emiten eventos relevantes
// y crea WebhookDeliveries para los endpoints activos de cada tenant.
package nats

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// minimalEvent extrae solo los campos comunes de cualquier evento de dominio.
type minimalEvent struct {
	TenantID string    `json:"tenant_id"`
	EventID  string    `json:"id"` // algunos eventos usan "id", otros "event_id"
	// Intentamos ambos nombres para compatibilidad.
	EventID2   string `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// EventConsumer suscribe a todos los streams y crea deliveries para cada evento.
type EventConsumer struct {
	consumer      eventbus.Consumer
	dispatchEvent *application.DispatchEventUseCase
	logger        *slog.Logger
}

func NewEventConsumer(
	consumer eventbus.Consumer,
	dispatchEvent *application.DispatchEventUseCase,
	logger *slog.Logger,
) *EventConsumer {
	return &EventConsumer{consumer: consumer, dispatchEvent: dispatchEvent, logger: logger}
}

// startConsumer arranca un consumer para un stream/filter específico.
func (c *EventConsumer) startConsumer(ctx context.Context, stream, consumerName, filter string) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        stream,
		Name:          consumerName,
		FilterSubject: filter,
		MaxDeliver:    3,
	}, c.handle)
}

func (c *EventConsumer) StartPaymentConsumer(ctx context.Context) error {
	return c.startConsumer(ctx, "PAYMENT", "webhook-payment-consumer", "payment.>")
}

func (c *EventConsumer) StartPayoutConsumer(ctx context.Context) error {
	return c.startConsumer(ctx, "PAYOUT", "webhook-payout-consumer", "payout.>")
}

func (c *EventConsumer) StartOnboardingConsumer(ctx context.Context) error {
	return c.startConsumer(ctx, "ONBOARDING", "webhook-onboarding-consumer", "onboarding.>")
}

func (c *EventConsumer) StartAuthConsumer(ctx context.Context) error {
	return c.startConsumer(ctx, "AUTH", "webhook-auth-consumer", "auth.tenant.>")
}

// handle procesa un mensaje entrante: extrae el TenantID y crea las deliveries.
func (c *EventConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var ev minimalEvent
	if err := json.Unmarshal(msg.Payload, &ev); err != nil {
		c.logger.Warn("webhook consumer: unmarshal failed, skipping",
			"subject", msg.Subject, "error", err)
		return nil // ack: no reintentamos eventos malformados
	}

	if ev.TenantID == "" {
		return nil // evento de plataforma sin tenant, ignorar
	}

	// Normalizar el eventID (algunos eventos usan "id", otros "event_id")
	eventID := ev.EventID
	if eventID == "" {
		eventID = ev.EventID2
	}
	if eventID == "" {
		eventID = msg.ID // fallback al ID del mensaje NATS
	}

	occurredAt := ev.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	if err := c.dispatchEvent.Execute(ctx, application.IncomingEvent{
		TenantID:   ev.TenantID,
		EventType:  msg.Subject,
		EventID:    eventID,
		OccurredAt: occurredAt,
		Data:       msg.Payload,
	}); err != nil {
		c.logger.Error("webhook consumer: dispatch event failed",
			"subject", msg.Subject,
			"tenant_id", ev.TenantID,
			"error", err,
		)
		return err // Nak → reintento NATS
	}

	return nil
}
