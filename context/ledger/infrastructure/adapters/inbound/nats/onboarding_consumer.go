// Package nats contiene consumers de eventos externos para el contexto Ledger.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

type onboardingApprovedPayload struct {
	TenantID         string `json:"tenant_id"`
	BusinessCategory string `json:"business_category"`
	Currency         string `json:"currency"`
}

// OnboardingConsumer crea las cuentas contables del comercio al aprobarse el KYC.
type OnboardingConsumer struct {
	consumer      eventbus.Consumer
	createAccount *application.CreateAccountUseCase
	logger        *slog.Logger
}

func NewOnboardingConsumer(
	consumer eventbus.Consumer,
	createAccount *application.CreateAccountUseCase,
	logger *slog.Logger,
) *OnboardingConsumer {
	return &OnboardingConsumer{
		consumer:      consumer,
		createAccount: createAccount,
		logger:        logger,
	}
}

// Start arranca el consumer. Bloquea hasta que ctx sea cancelado.
func (c *OnboardingConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "ONBOARDING",
		Name:          "ledger-onboarding-consumer",
		FilterSubject: "onboarding.application.approved.v1",
		MaxDeliver:    5,
	}, c.handle)
}

func (c *OnboardingConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var payload onboardingApprovedPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("ledger onboarding consumer: unmarshal: %w", err)
	}

	currency := payload.Currency
	if currency == "" {
		currency = "ARS"
	}

	// Crear cuentas contables estándar para el comercio aprobado.
	accounts := []struct{ accountType, desc string }{
		{"merchant_balance", "Saldo disponible del comercio"},
		{"reserve", "Reserva de garantía (rolling reserve)"},
		{"dispute_hold", "Fondos retenidos por disputas"},
	}

	for _, acc := range accounts {
		if _, err := c.createAccount.Execute(ctx, application.CreateAccountCmd{
			TenantID:    payload.TenantID,
			AccountType: acc.accountType,
			Currency:    currency,
			Description: acc.desc,
		}); err != nil {
			c.logger.Error("ledger onboarding consumer: create account failed",
				"tenantID", payload.TenantID,
				"accountType", acc.accountType,
				"error", err)
			return fmt.Errorf("create account %s: %w", acc.accountType, err)
		}
	}

	c.logger.Info("ledger onboarding consumer: accounts created",
		"tenantID", payload.TenantID,
		"currency", currency,
	)
	return nil
}
