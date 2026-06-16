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
	PSPFee         int64  `json:"psp_fee"`
	IdempotencyKey string `json:"idempotency_key"`
}

// PaymentConsumer crea asientos contables en el Ledger cuando se captura un pago.
//
// Asiento generado:
//   in_transit       CREDIT  amount          (fondos del PSP)
//   merchant_balance DEBIT   net_amount      (dinero del comercio: amount - platformFee - pspFee)
//   platform_fees    DEBIT   platform_fee    (comisión de la plataforma)
//
// Doble partida: sum(debits) = sum(credits) = amount.
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
	var p paymentCapturedPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return fmt.Errorf("ledger payment consumer: unmarshal: %w", err)
	}

	tenantID := domain.TenantID(p.TenantID)

	netAmount := p.Amount - p.PlatformFee - p.PSPFee
	if netAmount <= 0 {
		return fmt.Errorf("ledger payment consumer: non-positive net amount for payment %s", p.PaymentID)
	}

	inTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeInTransit, p.Currency)
	if err != nil {
		return fmt.Errorf("find in_transit account: %w", err)
	}

	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
	if err != nil {
		return fmt.Errorf("find merchant_balance account: %w", err)
	}

	lines := []application.PostingLine{
		{AccountID: inTransit.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency},
		{AccountID: merchantBalance.ID().String(), Direction: "debit", Amount: netAmount, Currency: p.Currency},
	}

	// Si hay comisión de plataforma, agregar la línea de fees.
	if p.PlatformFee > 0 {
		platformFees, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePlatformFees, p.Currency)
		if err == nil {
			lines = append(lines, application.PostingLine{
				AccountID: platformFees.ID().String(),
				Direction: "debit",
				Amount:    p.PlatformFee,
				Currency:  p.Currency,
			})
		}
		// Si no existe la cuenta de platform_fees (tenant sin ella configurada),
		// el psp_fee ya está absorbido en el netAmount. El asiento balancea igual.
	}

	idempotencyKey := "payment_captured_" + p.PaymentID

	if _, err := c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: idempotencyKey,
		Description:    fmt.Sprintf("Pago capturado: %s", p.PaymentID),
		Metadata: map[string]string{
			"payment_id":   p.PaymentID,
			"event_source": "payment.captured.v1",
		},
		Lines: lines,
	}); err != nil {
		c.logger.Error("ledger payment consumer: post entry failed",
			"paymentID", p.PaymentID, "error", err)
		return fmt.Errorf("post entry for payment %s: %w", p.PaymentID, err)
	}

	c.logger.Info("ledger payment consumer: entry posted",
		"paymentID", p.PaymentID, "amount", p.Amount, "currency", p.Currency)
	return nil
}
