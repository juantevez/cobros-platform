package nats

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// EventConsumer dispara las capacidades de AML a partir de eventos de dominio:
//   - ONBOARDING (onboarding.application.submitted.v1) → screening de watchlist
//   - PAYMENT    (payment.captured.v1)                 → monitoreo transaccional
type EventConsumer struct {
	consumer eventbus.Consumer
	screen   *application.ScreenApplicationUseCase
	monitor  *application.MonitorTransactionUseCase
	logger   *slog.Logger
}

func NewEventConsumer(
	consumer eventbus.Consumer,
	screen *application.ScreenApplicationUseCase,
	monitor *application.MonitorTransactionUseCase,
	logger *slog.Logger,
) *EventConsumer {
	return &EventConsumer{consumer: consumer, screen: screen, monitor: monitor, logger: logger}
}

func (c *EventConsumer) StartOnboardingConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "ONBOARDING", Name: "compliance-onboarding-consumer",
		FilterSubject: "onboarding.application.submitted.>", MaxDeliver: 5,
	}, c.handleOnboarding)
}

func (c *EventConsumer) StartPaymentConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream: "PAYMENT", Name: "compliance-payment-consumer",
		FilterSubject: "payment.captured.>", MaxDeliver: 5,
	}, c.handlePayment)
}

type applicationSubmitted struct {
	ApplicationID string `json:"application_id"`
	TenantID      string `json:"tenant_id"`
	LegalName     string `json:"legal_name"`
}

type paymentCaptured struct {
	PaymentID     string `json:"payment_id"`
	TenantID      string `json:"tenant_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	PaymentMethod string `json:"payment_method"`
}

func (c *EventConsumer) handleOnboarding(ctx context.Context, msg *eventbus.Message) error {
	var p applicationSubmitted
	if err := json.Unmarshal(msg.Payload, &p); err != nil || p.TenantID == "" {
		return nil // payload malformado o sin tenant → ack
	}
	if err := c.screen.Execute(ctx, application.ScreenApplicationCmd{
		TenantID:      p.TenantID,
		ApplicationID: p.ApplicationID,
		LegalName:     p.LegalName,
	}); err != nil {
		c.logger.Error("compliance: screening failed",
			"application_id", p.ApplicationID, "error", err)
		return err // nak → reintento
	}
	return nil
}

func (c *EventConsumer) handlePayment(ctx context.Context, msg *eventbus.Message) error {
	var p paymentCaptured
	if err := json.Unmarshal(msg.Payload, &p); err != nil || p.TenantID == "" {
		return nil
	}
	if err := c.monitor.Execute(ctx, application.MonitorTransactionCmd{
		TenantID:      p.TenantID,
		PaymentID:     p.PaymentID,
		Amount:        p.Amount,
		Currency:      p.Currency,
		PaymentMethod: p.PaymentMethod,
	}); err != nil {
		c.logger.Error("compliance: transaction monitoring failed",
			"payment_id", p.PaymentID, "error", err)
		return err
	}
	return nil
}
