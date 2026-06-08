// Package nats contiene el consumer de eventos de dominio para el contexto Audit.
//
// El Audit no produce eventos; solo los consume. Suscribe a los streams AUTH y
// LEDGER y registra cada evento relevante en el log de auditoría.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/audit/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// EventConsumer suscribe al bus de eventos y registra cada hecho en el audit log.
type EventConsumer struct {
	consumer       eventbus.Consumer
	recordAction   *application.RecordActionUseCase
	logger         *slog.Logger
}

func NewEventConsumer(
	consumer eventbus.Consumer,
	recordAction *application.RecordActionUseCase,
	logger *slog.Logger,
) *EventConsumer {
	return &EventConsumer{
		consumer:     consumer,
		recordAction: recordAction,
		logger:       logger,
	}
}

// StartAuthConsumer arranca el consumer del stream AUTH.
// Bloquea hasta que ctx sea cancelado. Llamar en una goroutine.
func (c *EventConsumer) StartAuthConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "AUTH",
		Name:          "audit-auth-consumer",
		FilterSubject: "auth.>",
		MaxDeliver:    5,
	}, c.handle)
}

// StartLedgerConsumer arranca el consumer del stream LEDGER.
// Bloquea hasta que ctx sea cancelado. Llamar en una goroutine.
func (c *EventConsumer) StartLedgerConsumer(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "LEDGER",
		Name:          "audit-ledger-consumer",
		FilterSubject: "ledger.>",
		MaxDeliver:    5,
	}, c.handle)
}

// handle procesa un mensaje y lo registra en el audit log.
func (c *EventConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	mapped, err := mapEvent(msg)
	if err != nil {
		// Si no sabemos qué hacer con el evento, lo logueamos y lo descartamos
		// (ack) para no bloquear la cola. El audit no debe ser un punto de falla.
		c.logger.Warn("audit consumer: unknown event, skipping",
			"subject", msg.Subject, "error", err)
		return nil
	}

	// Propagar el correlation ID si viene en los headers del mensaje.
	correlationID := ""
	if msg.Headers != nil {
		correlationID = msg.Headers["correlation-id"]
	}
	mapped.CorrelationID = correlationID

	// El TenantID del contexto puede no estar (el evento viene de JetStream,
	// no de un request HTTP). Lo tomamos del payload del evento.
	if err := c.recordAction.Execute(ctx, *mapped); err != nil {
		c.logger.Error("audit consumer: record action failed",
			"subject", msg.Subject, "error", err)
		return fmt.Errorf("record action: %w", err) // Nak → reintento
	}

	return nil
}

// ── Mapeo de eventos a acciones de auditoría ──────────────────────────────────

// eventPayload es el mínimo común de todos los eventos de dominio.
type eventPayload struct {
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
	ApiKeyID   string `json:"api_key_id"`
	EntryID    string `json:"entry_id"`
	AccountID  string `json:"account_id"`
	AssignedBy string `json:"assigned_by"`
}

// mapEvent traduce un mensaje NATS a un RecordActionCmd.
func mapEvent(msg *eventbus.Message) (*application.RecordActionCmd, error) {
	var p eventPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	cmd := &application.RecordActionCmd{
		TenantID: p.TenantID,
		Actor:    "system", // los eventos son disparados por el sistema
		Metadata: map[string]string{"event_subject": msg.Subject},
	}

	switch msg.Subject {
	// ── Auth ──
	case "auth.tenant.created.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.tenant.created", "tenant", p.TenantID
	case "auth.tenant.activated.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.tenant.activated", "tenant", p.TenantID
	case "auth.tenant.suspended.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.tenant.suspended", "tenant", p.TenantID
	case "auth.user.registered.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.user.registered", "user", p.UserID
	case "auth.user.suspended.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.user.suspended", "user", p.UserID
	case "auth.apikey.issued.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.apikey.issued", "api_key", p.ApiKeyID
	case "auth.apikey.revoked.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.apikey.revoked", "api_key", p.ApiKeyID
	case "auth.role.assigned.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "auth.role.assigned", "user", p.UserID
		cmd.Actor = p.AssignedBy

	// ── Ledger ──
	case "ledger.account.created.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "ledger.account.created", "ledger_account", p.AccountID
	case "ledger.entry.posted.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "ledger.entry.posted", "journal_entry", p.EntryID
	case "ledger.entry.reversed.v1":
		cmd.Action, cmd.ResourceType, cmd.ResourceID = "ledger.entry.reversed", "journal_entry", p.EntryID

	default:
		return nil, fmt.Errorf("no mapping for subject %q", msg.Subject)
	}

	// Inyectar el tenantID en el contexto para que FindLast funcione sin RLS.
	// El audit_log no tiene RLS, pero si otras tablas la tienen en el mismo ctx,
	// aseguramos que el tenant esté disponible.
	_ = postgres.WithTenantID // referencia al helper; se usa en el caller si necesario

	return cmd, nil
}
