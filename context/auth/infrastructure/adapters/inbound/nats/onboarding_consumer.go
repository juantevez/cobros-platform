// Package nats contiene consumers de eventos externos para el contexto Auth.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// onboardingApprovedPayload es el payload del evento onboarding.application.approved.v1
type onboardingApprovedPayload struct {
	TenantID         string `json:"tenant_id"`
	BusinessCategory string `json:"business_category"`
	Currency         string `json:"currency"`
}

// OnboardingConsumer reacciona a eventos del contexto Onboarding.
type OnboardingConsumer struct {
	consumer       eventbus.Consumer
	activateTenant *application.ActivateTenantUseCase
	logger         *slog.Logger
}

func NewOnboardingConsumer(
	consumer eventbus.Consumer,
	activateTenant *application.ActivateTenantUseCase,
	logger *slog.Logger,
) *OnboardingConsumer {
	return &OnboardingConsumer{
		consumer:       consumer,
		activateTenant: activateTenant,
		logger:         logger,
	}
}

// Start arranca el consumer. Bloquea hasta que ctx sea cancelado.
func (c *OnboardingConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "ONBOARDING",
		Name:          "auth-onboarding-consumer",
		FilterSubject: "onboarding.application.approved.v1",
		MaxDeliver:    5,
	}, c.handle)
}

func (c *OnboardingConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var payload onboardingApprovedPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("auth onboarding consumer: unmarshal: %w", err)
	}

	// Al aprobarse el KYC, activamos el Tenant en modo producción.
	if err := c.activateTenant.Execute(ctx, application.ActivateTenantCmd{
		TenantID:    payload.TenantID,
		Environment: "production",
	}); err != nil {
		c.logger.Error("auth onboarding consumer: activate tenant failed",
			"tenantID", payload.TenantID, "error", err)
		return fmt.Errorf("activate tenant: %w", err)
	}

	c.logger.Info("auth onboarding consumer: tenant activated",
		"tenantID", payload.TenantID)
	return nil
}
