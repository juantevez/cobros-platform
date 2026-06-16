package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

type paymentCapturedPayload struct {
	PaymentID      string `json:"payment_id"`
	TenantID       string `json:"tenant_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	PlatformFee    int64  `json:"platform_fee"`
	IdempotencyKey string `json:"idempotency_key"`
}

// PaymentConsumer registra asientos contables cuando se captura un pago.
//
// Asiento generado (doble partida):
//
//	in_transit       CREDIT  amount
//	merchant_balance DEBIT   amount - platform_fee
//	platform_fees    DEBIT   platform_fee  (solo si platform_fee > 0)
type PaymentConsumer struct {
	consumer    eventbus.Consumer
	postEntry   *application.PostEntryUseCase
	accountRepo application.AccountRepository
	logger      *slog.Logger
}

func NewPaymentConsumer(
	consumer eventbus.Consumer,
	postEntry *application.PostEntryUseCase,
	accountRepo application.AccountRepository,
	logger *slog.Logger,
) *PaymentConsumer {
	return &PaymentConsumer{
		consumer:    consumer,
		postEntry:   postEntry,
		accountRepo: accountRepo,
		logger:      logger,
	}
}

func (c *PaymentConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "PAYMENT",
		Name:          "ledger-payment-consumer",
		FilterSubject: "payment.captured.v1",
		MaxDeliver:    5,
	}, c.handle)
}

func (c *PaymentConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var payload paymentCapturedPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("ledger payment consumer: unmarshal: %w", err)
	}

	tenantID, err := domain.ParseTenantID(payload.TenantID)
	if err != nil {
		return fmt.Errorf("ledger payment consumer: parse tenant id: %w", err)
	}

	inTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeInTransit, payload.Currency)
	if err != nil {
		return fmt.Errorf("ledger payment consumer: find in_transit account: %w", err)
	}

	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, payload.Currency)
	if err != nil {
		return fmt.Errorf("ledger payment consumer: find merchant_balance account: %w", err)
	}

	merchantAmount := payload.Amount - payload.PlatformFee
	lines := []application.PostingLine{
		{AccountID: inTransit.ID().String(), Direction: "credit", Amount: payload.Amount, Currency: payload.Currency},
		{AccountID: merchantBalance.ID().String(), Direction: "debit", Amount: merchantAmount, Currency: payload.Currency},
	}

	if payload.PlatformFee > 0 {
		platformFees, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePlatformFees, payload.Currency)
		if err != nil {
			return fmt.Errorf("ledger payment consumer: find platform_fees account: %w", err)
		}
		lines = append(lines, application.PostingLine{
			AccountID: platformFees.ID().String(),
			Direction: "debit",
			Amount:    payload.PlatformFee,
			Currency:  payload.Currency,
		})
	}

	if _, err := c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       payload.TenantID,
		IdempotencyKey: payload.IdempotencyKey,
		Description:    fmt.Sprintf("Payment captured: %s", payload.PaymentID),
		Metadata:       map[string]string{"payment_id": payload.PaymentID},
		Lines:          lines,
	}); err != nil {
		return fmt.Errorf("ledger payment consumer: post entry: %w", err)
	}

	c.logger.Info("ledger payment consumer: entry posted",
		"paymentID", payload.PaymentID,
		"tenantID", payload.TenantID,
		"amount", payload.Amount,
	)
	return nil
}
