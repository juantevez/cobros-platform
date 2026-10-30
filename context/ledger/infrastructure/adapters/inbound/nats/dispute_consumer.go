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

type disputeEventPayload struct {
	DisputeID string `json:"dispute_id"`
	PaymentID string `json:"payment_id"`
	TenantID  string `json:"tenant_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Outcome   string `json:"outcome"`
}

// DisputeConsumer registra los asientos contables para disputas.
//
// dispute.opened.v1:
//   merchant_balance DEBIT   amount   (congelar: sale del saldo disponible)
//   dispute_hold     CREDIT  amount   (fondos en disputa)
//
// dispute.resolved.v1 outcome=won:
//   dispute_hold     DEBIT   amount   (liberar: sale del hold)
//   merchant_balance CREDIT  amount   (vuelven al saldo disponible)
//
// dispute.resolved.v1 outcome=lost/accepted/expired:
//   dispute_hold     DEBIT   amount   (liberar: sale del hold)
//   in_transit       CREDIT  amount   (el PSP descuenta el contracargo de la liquidación)
type DisputeConsumer struct {
	consumer    eventbus.Consumer
	postEntry   *application.PostEntryUseCase
	accountRepo application.AccountRepository
	logger      *slog.Logger
}

func NewDisputeConsumer(
	consumer eventbus.Consumer,
	postEntry *application.PostEntryUseCase,
	accountRepo application.AccountRepository,
	logger *slog.Logger,
) *DisputeConsumer {
	return &DisputeConsumer{consumer: consumer, postEntry: postEntry,
		accountRepo: accountRepo, logger: logger}
}

func (c *DisputeConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "DISPUTE",
		Name:          "ledger-dispute-consumer",
		FilterSubject: "dispute.>",
		MaxDeliver:    5,
	}, strict(c.handle))
}

func (c *DisputeConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var p disputeEventPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return eventbus.Permanent(fmt.Errorf("dispute consumer: unmarshal: %w", err))
	}
	ctx = pkgpostgres.WithTenantID(ctx, p.TenantID) // RLS de las escrituras

	tenantID, err := domain.ParseTenantID(p.TenantID)
	if err != nil {
		return eventbus.Permanent(err)
	}

	switch msg.Subject {
	case "dispute.opened.v1":
		return c.postOpened(ctx, tenantID, p)
	case "dispute.resolved.v1":
		return c.postResolved(ctx, tenantID, p)
	}
	return nil
}

func (c *DisputeConsumer) postOpened(ctx context.Context, tenantID domain.TenantID, p disputeEventPayload) error {
	merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
	if err != nil {
		return fmt.Errorf("find merchant_balance: %w", err)
	}
	disputeHold, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeDisputeHold, p.Currency)
	if err != nil {
		return fmt.Errorf("find dispute_hold: %w", err)
	}

	_, err = c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: "dispute_opened_" + p.DisputeID,
		Description:    fmt.Sprintf("Disputa abierta: %s", p.DisputeID),
		Metadata:       map[string]string{"dispute_id": p.DisputeID, "payment_id": p.PaymentID},
		Lines: []application.PostingLine{
			{AccountID: merchantBalance.ID().String(), Direction: "debit", Amount: p.Amount, Currency: p.Currency},
			{AccountID: disputeHold.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency},
		},
	})
	return err
}

func (c *DisputeConsumer) postResolved(ctx context.Context, tenantID domain.TenantID, p disputeEventPayload) error {
	disputeHold, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeDisputeHold, p.Currency)
	if err != nil {
		return fmt.Errorf("find dispute_hold: %w", err)
	}

	lines := []application.PostingLine{
		{AccountID: disputeHold.ID().String(), Direction: "debit", Amount: p.Amount, Currency: p.Currency},
	}

	switch p.Outcome {
	case "won":
		// El comercio ganó: los fondos vuelven a su saldo disponible.
		merchantBalance, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeMerchantBalance, p.Currency)
		if err != nil {
			return fmt.Errorf("find merchant_balance: %w", err)
		}
		lines = append(lines, application.PostingLine{
			AccountID: merchantBalance.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency,
		})
	case "lost", "accepted", "expired":
		// El comercio perdió / aceptó / expiró: el PSP le devuelve el monto al
		// tarjetahabiente y lo descuenta de lo que nos liquida.
		inTransit, err := c.accountRepo.FindByTenantAndType(ctx, tenantID, domain.AccountTypeInTransit, p.Currency)
		if err != nil {
			return fmt.Errorf("find in_transit: %w", err)
		}
		lines = append(lines, application.PostingLine{
			AccountID: inTransit.ID().String(), Direction: "credit", Amount: p.Amount, Currency: p.Currency,
		})
	default:
		// Asentarlo como perdida movería dinero por un desenlace que no existe.
		return eventbus.Permanent(fmt.Errorf("dispute consumer: unknown outcome %q for dispute %s", p.Outcome, p.DisputeID))
	}

	_, err = c.postEntry.Execute(ctx, application.PostEntryCmd{
		TenantID:       p.TenantID,
		IdempotencyKey: "dispute_resolved_" + p.DisputeID,
		Description:    fmt.Sprintf("Disputa resuelta (%s): %s", p.Outcome, p.DisputeID),
		Metadata:       map[string]string{"dispute_id": p.DisputeID, "outcome": p.Outcome},
		Lines:          lines,
	})
	return err
}
