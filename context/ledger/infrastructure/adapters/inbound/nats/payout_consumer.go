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

// PayoutConsumer registra los asientos contables para cada evento del ciclo de payout.
//
// Asientos generados:
//
//	payout.initiated.v1:
//	  merchant_balance   CREDIT  amount   (sale del saldo del comercio)
//	  payout_transit     DEBIT   amount   (fondos en camino al banco)
//
//	payout.confirmed.v1:
//	  payout_transit     CREDIT  amount   (salen del tránsito)
//	  payout_sent        DEBIT   amount   (fondos efectivamente enviados)
//
//	payout.failed.v1 → reversa del asiento de initiated:
//	  payout_transit     CREDIT  amount   (revierten del tránsito)
//	  merchant_balance   DEBIT   amount   (vuelven al saldo del comercio)
type PayoutConsumer struct {
	consumer    eventbus.Consumer
	postEntry   *application.PostEntryUseCase
	accountRepo application.AccountRepository
	logger      *slog.Logger
}

func NewPayoutConsumer(
	consumer eventbus.Consumer,
	postEntry *application.PostEntryUseCase,
	accountRepo application.AccountRepository,
	logger *slog.Logger,
) *PayoutConsumer {
	return &PayoutConsumer{consumer: consumer, postEntry: postEntry, accountRepo: accountRepo, logger: logger}
}

func (c *PayoutConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "PAYOUT",
		Name:          "ledger-payout-consumer",
		FilterSubject: "payout.>",
		MaxDeliver:    5,
	}, c.handle)
}

type payoutEventPayload struct {
	PayoutID       string `json:"payout_id"`
	TenantID       string `json:"tenant_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"`
	BankReference  string `json:"bank_reference"`
	FailureReason  string `json:"failure_reason"`
}

func (c *PayoutConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var p payoutEventPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return fmt.Errorf("payout consumer: unmarshal: %w", err)
	}

	tenantID := domain.TenantID(p.TenantID)

	switch msg.Subject {
	case "payout.initiated.v1":
		return c.postInitiated(ctx, tenantID, p)
	case "payout.confirmed.v1":
		return c.postConfirmed(ctx, tenantID, p)
	case "payout.failed.v1":
		return c.postFailed(ctx, tenantID, p)
	default:
		return nil // subject desconocido: ignorar
	}
}

func (c *PayoutConsumer) postInitiated(ctx context.Context, tenantID domain.TenantID, p payoutEventPayload) error {
	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
	if err != nil {
		return fmt.Errorf("find merchant_balance: %w", err)
	}
	payoutTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePayoutTransit, p.Currency)
	if err != nil {
		return fmt.Errorf("find payout_transit: %w", err)
	}

	idempKey := p.IdempotencyKey
	if idempKey == "" {
		idempKey = "payout_initiated_" + p.PayoutID
	}

	_, err = c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: idempKey,
		Description:    fmt.Sprintf("Desembolso iniciado: %s", p.PayoutID),
		Metadata:       map[string]string{"payout_id": p.PayoutID, "event": "payout.initiated.v1"},
		Lines: []application.PostingLine{
			{AccountID: merchantBalance.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency},
			{AccountID: payoutTransit.ID().String(), Direction: "debit", Amount: p.Amount, Currency: p.Currency},
		},
	})
	return err
}

func (c *PayoutConsumer) postConfirmed(ctx context.Context, tenantID domain.TenantID, p payoutEventPayload) error {
	payoutTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePayoutTransit, p.Currency)
	if err != nil {
		return fmt.Errorf("find payout_transit: %w", err)
	}
	payoutSent, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePayoutSent, p.Currency)
	if err != nil {
		return fmt.Errorf("find payout_sent: %w", err)
	}

	_, err = c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: "payout_confirmed_" + p.PayoutID,
		Description:    fmt.Sprintf("Desembolso confirmado: %s", p.PayoutID),
		Metadata:       map[string]string{"payout_id": p.PayoutID, "bank_reference": p.BankReference},
		Lines: []application.PostingLine{
			{AccountID: payoutTransit.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency},
			{AccountID: payoutSent.ID().String(), Direction: "debit", Amount: p.Amount, Currency: p.Currency},
		},
	})
	return err
}

func (c *PayoutConsumer) postFailed(ctx context.Context, tenantID domain.TenantID, p payoutEventPayload) error {
	// Reversa: los fondos vuelven de payout_transit a merchant_balance.
	payoutTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypePayoutTransit, p.Currency)
	if err != nil {
		return fmt.Errorf("find payout_transit: %w", err)
	}
	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
	if err != nil {
		return fmt.Errorf("find merchant_balance: %w", err)
	}

	_, err = c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: "payout_failed_" + p.PayoutID,
		Description:    fmt.Sprintf("Reversa desembolso fallido: %s", p.PayoutID),
		Metadata:       map[string]string{"payout_id": p.PayoutID, "failure_reason": p.FailureReason},
		Lines: []application.PostingLine{
			{AccountID: payoutTransit.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency},
			{AccountID: merchantBalance.ID().String(), Direction: "debit", Amount: p.Amount, Currency: p.Currency},
		},
	})
	return err
}
