package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/notification/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// eventPayload extrae los campos comunes de los eventos de dominio.
type eventPayload struct {
	TenantID        string `json:"tenant_id"`
	PaymentID       string `json:"payment_id"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	FailureReason   string `json:"failure_reason"`
	PaymentMethod   string `json:"payment_method"`
	BankReference   string `json:"bank_reference"`
	RejectionReason string `json:"rejection_reason"`
}

// EventConsumer traduce eventos NATS en comandos de notificación.
type EventConsumer struct {
	consumer eventbus.Consumer
	sendNotif *application.SendNotificationUseCase
	logger    *slog.Logger
}

func NewEventConsumer(
	consumer eventbus.Consumer,
	sendNotif *application.SendNotificationUseCase,
	logger *slog.Logger,
) *EventConsumer {
	return &EventConsumer{consumer: consumer, sendNotif: sendNotif, logger: logger}
}

func (c *EventConsumer) StartPaymentConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "PAYMENT", Name: "notification-payment-consumer",
		FilterSubject: "payment.>", MaxDeliver: 3,
	}, c.handle)
}

func (c *EventConsumer) StartPayoutConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "PAYOUT", Name: "notification-payout-consumer",
		FilterSubject: "payout.>", MaxDeliver: 3,
	}, c.handle)
}

func (c *EventConsumer) StartOnboardingConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "ONBOARDING", Name: "notification-onboarding-consumer",
		FilterSubject: "onboarding.application.>", MaxDeliver: 3,
	}, c.handle)
}

func (c *EventConsumer) StartAuthConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "AUTH", Name: "notification-auth-consumer",
		FilterSubject: "auth.tenant.suspended.>", MaxDeliver: 3,
	}, c.handle)
}

func (c *EventConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var p eventPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil || p.TenantID == "" {
		return nil // ack silencioso: evento sin tenant o malformado
	}

	data := buildTemplateData(msg.Subject, p)
	if data == nil {
		return nil // evento sin template → skip
	}

	if err := c.sendNotif.Execute(ctx, application.SendNotificationCmd{
		TenantID:     p.TenantID,
		EventType:    msg.Subject,
		TemplateData: data,
	}); err != nil {
		c.logger.Error("notification consumer: send failed",
			"subject", msg.Subject,
			"tenant_id", p.TenantID,
			"error", err,
		)
		// No retornamos el error → ack (las notificaciones no son críticas).
	}

	return nil
}

// buildTemplateData construye el mapa de datos para el template según el evento.
func buildTemplateData(subject string, p eventPayload) map[string]string {
	amountStr := fmt.Sprintf("%.2f", float64(p.Amount)/100)

	switch subject {
	case "payment.captured.v1":
		return map[string]string{
			"payment_id":     p.PaymentID,
			"amount":         amountStr,
			"currency":       p.Currency,
			"payment_method": p.PaymentMethod,
		}
	case "payment.failed.v1", "payment.risk_rejected.v1":
		return map[string]string{
			"payment_id":     p.PaymentID,
			"amount":         amountStr,
			"currency":       p.Currency,
			"failure_reason": p.FailureReason,
		}
	case "payment.refunded.v1":
		return map[string]string{
			"payment_id": p.PaymentID,
			"amount":     amountStr,
			"currency":   p.Currency,
		}
	case "payout.confirmed.v1":
		return map[string]string{
			"amount":         amountStr,
			"currency":       p.Currency,
			"bank_reference": p.BankReference,
		}
	case "payout.failed.v1":
		return map[string]string{
			"amount":         amountStr,
			"currency":       p.Currency,
			"failure_reason": p.FailureReason,
		}
	case "onboarding.application.approved.v1":
		return map[string]string{}
	case "onboarding.application.rejected.v1":
		return map[string]string{
			"rejection_reason": p.RejectionReason,
		}
	case "auth.tenant.suspended.v1":
		return map[string]string{}
	}
	return nil // sin template para este subject
}
