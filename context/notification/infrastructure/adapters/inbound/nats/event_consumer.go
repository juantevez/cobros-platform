package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/notification/application"
	"github.com/juantevez/cobros-platform/context/notification/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// eventPayload extrae los campos comunes de los eventos de dominio.
type eventPayload struct {
	TenantID        string `json:"tenant_id"`
	PaymentID       string `json:"payment_id"`
	Amount          int64  `json:"amount"`
	RefundAmount    int64  `json:"refund_amount"` // PaymentRefundedEvent usa refund_amount, no amount
	Currency        string `json:"currency"`
	FailureReason   string `json:"failure_reason"`
	PaymentMethod   string `json:"payment_method"`
	BankReference   string `json:"bank_reference"`
	RejectionReason string `json:"rejection_reason"`
	// Disputas (dispute.opened / dispute.resolved).
	Reason   string    `json:"reason"`
	Outcome  string    `json:"outcome"`
	Deadline time.Time `json:"deadline"`
}

// EventConsumer traduce eventos NATS en comandos de notificación.
type EventConsumer struct {
	consumer  eventbus.Consumer
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

// StartDisputeConsumer avisa al comercio de las disputas abiertas (con su
// plazo para responder) y de cómo se cerraron.
func (c *EventConsumer) StartDisputeConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "DISPUTE", Name: "notification-dispute-consumer",
		FilterSubject: "dispute.>", MaxDeliver: 3,
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
		// ID del mensaje NATS (Nats-Msg-Id = EventID del evento de dominio):
		// clave natural de dedupe/idempotencia del consumo.
		NatsMsgID: msg.ID,
	}); err != nil {
		if errors.Is(err, domain.ErrInvalidTenantID) {
			// Error permanente: reentregar no lo arregla (poison message) → ack.
			c.logger.Warn("notification consumer: invalid tenant id, skipping",
				"subject", msg.Subject,
				"tenant_id", p.TenantID,
			)
			return nil
		}
		c.logger.Error("notification consumer: send failed",
			"subject", msg.Subject,
			"tenant_id", p.TenantID,
			"error", err,
		)
		// Retornamos el error → nak: si falló la verificación de dedupe
		// (transitorio), la reentrega reintentará; el dedupe evita que el
		// reintento duplique el envío. Si el handler siempre ackeaba, un
		// fallo de BD impedía notificar jamás.
		return err
	}

	return nil
}

// handledSubjects son los subjects para los que buildTemplateData arma datos
// (y que por lo tanto deben tener template). Lo usa el test de consistencia
// del catálogo.
var handledSubjects = []string{
	"payment.captured.v1", "payment.failed.v1", "payment.refunded.v1",
	"payout.confirmed.v1", "payout.failed.v1", "payout.unknown.v1",
	"onboarding.application.approved.v1", "onboarding.application.rejected.v1",
	"auth.tenant.suspended.v1",
	"dispute.opened.v1", "dispute.resolved.v1",
}

// disputeOutcomes traduce el outcome de dispute.resolved.v1 para el email.
var disputeOutcomes = map[string]string{
	"won":      "ganada: los fondos retenidos vuelven a tu saldo",
	"lost":     "perdida: el banco devolvió el monto al pagador",
	"accepted": "aceptada: el monto se devolvió al pagador",
	"expired":  "vencida sin respuesta: el monto se devolvió al pagador",
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
	case "payment.failed.v1": // también cubre el rechazo por riesgo (Payment publica payment.failed)
		return map[string]string{
			"payment_id":     p.PaymentID,
			"amount":         amountStr,
			"currency":       p.Currency,
			"failure_reason": p.FailureReason,
		}
	case "payment.refunded.v1":
		// PaymentRefundedEvent publica refund_amount (no amount): mapearlo a
		// la clave del template; si viniera 0, tolerar eventos que usen
		// "amount" directamente.
		refund := p.RefundAmount
		if refund == 0 {
			refund = p.Amount
		}
		return map[string]string{
			"payment_id": p.PaymentID,
			"amount":     fmt.Sprintf("%.2f", float64(refund)/100),
			"currency":   p.Currency,
		}
	case "payout.confirmed.v1":
		return map[string]string{
			"amount":         amountStr,
			"currency":       p.Currency,
			"bank_reference": p.BankReference,
		}
	case "payout.unknown.v1":
		// El banco no confirmó la transferencia: el comercio tiene que saber
		// que su retiro está en verificación (no exponemos el motivo técnico).
		return map[string]string{
			"amount":   amountStr,
			"currency": p.Currency,
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
	case "dispute.opened.v1":
		deadline := "sin fecha informada"
		if !p.Deadline.IsZero() {
			deadline = p.Deadline.UTC().Format("02/01/2006 15:04") + " (UTC)"
		}
		return map[string]string{
			"payment_id": p.PaymentID,
			"amount":     amountStr,
			"currency":   p.Currency,
			"reason":     p.Reason,
			"deadline":   deadline,
		}
	case "dispute.resolved.v1":
		outcome, ok := disputeOutcomes[p.Outcome]
		if !ok {
			outcome = p.Outcome
		}
		return map[string]string{
			"payment_id": p.PaymentID,
			"amount":     amountStr,
			"currency":   p.Currency,
			"outcome":    outcome,
		}
	}
	return nil // sin template para este subject
}
