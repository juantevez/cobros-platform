package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
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

type paymentRefundedPayload struct {
	PaymentID    string `json:"payment_id"`
	TenantID     string `json:"tenant_id"`
	RefundAmount int64  `json:"refund_amount"`
	Currency     string `json:"currency"`
}

// PaymentConsumer crea asientos contables en el Ledger cuando se captura o se
// reembolsa un pago.
//
// payment.captured.v1:
//   in_transit       DEBIT   amount - pspFee (lo que el PSP liquida)
//   merchant_balance CREDIT  net_amount      (dinero del comercio: amount - platformFee - pspFee)
//   platform_fees    CREDIT  platform_fee    (comisión de la plataforma, si hay)
//
// Doble partida: sum(debits) = sum(credits) = amount - pspFee. La comisión del
// PSP nunca entra al sistema: el PSP la retiene de la liquidación.
//
// Convención (Apply: crédito suma, débito resta): merchant_balance y
// platform_fees crecen con el pago; in_transit queda negativo mientras el PSP
// adeuda la liquidación.
//
// payment.refunded.v1 (total, uno por pago):
//   merchant_balance DEBIT   refund_amount   (el comercio devuelve el cobro)
//   in_transit       CREDIT  refund_amount   (el PSP lo descuenta de la liquidación)
//
// Las comisiones no se devuelven: el reembolso sale entero del saldo del comercio.
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
		FilterSubject: "payment.>",
		MaxDeliver:    5,
	}, c.handle)
}

func (c *PaymentConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	switch msg.Subject {
	case "payment.captured.v1":
		return c.handleCaptured(ctx, msg)
	case "payment.refunded.v1":
		return c.handleRefunded(ctx, msg)
	default:
		return nil // otros eventos de pago no mueven dinero
	}
}

func (c *PaymentConsumer) handleCaptured(ctx context.Context, msg *eventbus.Message) error {
	var p paymentCapturedPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return fmt.Errorf("ledger payment consumer: unmarshal: %w", err)
	}
	ctx = pkgpostgres.WithTenantID(ctx, p.TenantID) // RLS de las escrituras

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
		{AccountID: inTransit.ID().String(), Direction: "debit", Amount: p.Amount - p.PSPFee, Currency: p.Currency},
		{AccountID: merchantBalance.ID().String(), Direction: "credit", Amount: netAmount, Currency: p.Currency},
	}

	// Si hay comisión de plataforma, agregar la línea de fees. Sin la cuenta
	// el asiento no balancea: es un error, no una línea que se pueda omitir.
	if p.PlatformFee > 0 {
		platformFees, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePlatformFees, p.Currency)
		if err != nil {
			return fmt.Errorf("find platform_fees account: %w", err)
		}
		lines = append(lines, application.PostingLine{
			AccountID: platformFees.ID().String(),
			Direction: "credit",
			Amount:    p.PlatformFee,
			Currency:  p.Currency,
		})
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

func (c *PaymentConsumer) handleRefunded(ctx context.Context, msg *eventbus.Message) error {
	var p paymentRefundedPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return fmt.Errorf("ledger payment consumer: unmarshal refund: %w", err)
	}
	ctx = pkgpostgres.WithTenantID(ctx, p.TenantID) // RLS de las escrituras

	tenantID := domain.TenantID(p.TenantID)

	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
	if err != nil {
		return fmt.Errorf("find merchant_balance account: %w", err)
	}
	inTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeInTransit, p.Currency)
	if err != nil {
		return fmt.Errorf("find in_transit account: %w", err)
	}

	if _, err := c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: "payment_refunded_" + p.PaymentID,
		Description:    fmt.Sprintf("Pago reembolsado: %s", p.PaymentID),
		Metadata: map[string]string{
			"payment_id":   p.PaymentID,
			"event_source": "payment.refunded.v1",
		},
		Lines: []application.PostingLine{
			{AccountID: merchantBalance.ID().String(), Direction: "debit", Amount: p.RefundAmount, Currency: p.Currency},
			{AccountID: inTransit.ID().String(), Direction: "credit", Amount: p.RefundAmount, Currency: p.Currency},
		},
	}); err != nil {
		c.logger.Error("ledger payment consumer: post refund entry failed",
			"paymentID", p.PaymentID, "error", err)
		return fmt.Errorf("post refund entry for payment %s: %w", p.PaymentID, err)
	}

	c.logger.Info("ledger payment consumer: refund entry posted",
		"paymentID", p.PaymentID, "amount", p.RefundAmount, "currency", p.Currency)
	return nil
}
