// Package nats contiene el consumer de eventos de dominio para el contexto Audit.
//
// El Audit no produce eventos; solo los consume. Suscribe a los streams de los
// contextos con acciones sensibles y registra cada evento mapeado en el log
// de auditoría.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/application"
	"github.com/juantevez/cobros-platform/context/audit/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// Subscription es un consumer durable de Audit sobre un stream.
type Subscription struct {
	Stream string
	Filter string
	Name   string
}

// Subscriptions son los streams que audita la plataforma. Los filtros dejan
// afuera los eventos de alto volumen que no son acciones sensibles
// (payment.captured/failed: el movimiento de dinero ya queda auditado por
// ledger.entry.posted; webhook.delivery.*: operación interna).
var Subscriptions = []Subscription{
	{Stream: "AUTH", Filter: "auth.>", Name: "audit-auth-consumer"},
	{Stream: "LEDGER", Filter: "ledger.>", Name: "audit-ledger-consumer"},
	{Stream: "ONBOARDING", Filter: "onboarding.>", Name: "audit-onboarding-consumer"},
	{Stream: "PAYMENT", Filter: "payment.refunded.>", Name: "audit-payment-consumer"},
	{Stream: "PAYOUT", Filter: "payout.>", Name: "audit-payout-consumer"},
	{Stream: "BILLING", Filter: "billing.>", Name: "audit-billing-consumer"},
	{Stream: "DISPUTE", Filter: "dispute.>", Name: "audit-dispute-consumer"},
	{Stream: "WEBHOOK", Filter: "webhook.endpoint.>", Name: "audit-webhook-consumer"},
	{Stream: "COMPLIANCE", Filter: "compliance.>", Name: "audit-compliance-consumer"},
	{Stream: "RECONCILIATION", Filter: "reconciliation.>", Name: "audit-reconciliation-consumer"},
}

// EventConsumer suscribe al bus de eventos y registra cada hecho en el audit log.
type EventConsumer struct {
	consumer     eventbus.Consumer
	recordAction *application.RecordActionUseCase
	logger       *slog.Logger
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

// Start arranca el consumer de una suscripción. Bloquea hasta que ctx sea
// cancelado. Llamar en una goroutine por suscripción.
func (c *EventConsumer) Start(ctx context.Context, sub Subscription) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        sub.Stream,
		Name:          sub.Name,
		FilterSubject: sub.Filter,
		MaxDeliver:    5,
	}, c.handle)
}

// handle procesa un mensaje y lo registra en el audit log. Nunca descarta un
// evento: si no está en el catálogo o su payload no se puede leer, lo registra
// como audit.unmapped_event y lo loguea como error (el catálogo quedó atrás de
// los eventos que se publican). El audit no bloquea la cola: solo un fallo al
// escribir hace nak.
func (c *EventConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	mapped, err := mapEvent(msg)
	if err != nil {
		c.logger.Error("audit consumer: event not in catalog, recording as unmapped",
			"subject", msg.Subject, "event_id", msg.ID, "error", err)
		mapped = unmappedEvent(msg, err)
	}

	// Quién originó el hecho y el correlation ID de la request: los propaga
	// el outbox en los headers. Sin header (mensajes viejos), queda el actor
	// que haya resuelto mapEvent. Un actor declarado en el payload (login,
	// logout, asignación de rol) tiene prioridad: es el sujeto del hecho.
	if actor := msg.Headers[eventbus.HeaderActor]; actor != "" && !mapped.actorFromPayload {
		mapped.Actor = actor
	}
	mapped.CorrelationID = msg.Headers[eventbus.HeaderCorrelationID]
	// Origen: ID del evento (idempotencia ante re-entregas) y hora del hecho.
	mapped.EventID = msg.ID
	mapped.OccurredAt = occurredAt(msg)

	if err := c.recordAction.Execute(ctx, mapped.RecordActionCmd); err != nil {
		c.logger.Error("audit consumer: record action failed",
			"subject", msg.Subject, "error", err)
		return fmt.Errorf("record action: %w", err) // Nak → reintento
	}

	return nil
}

// occurredAt es la hora real del evento (header occurred-at del relay del
// outbox). Cero si falta o es inválido: se usa la hora de registro.
func occurredAt(msg *eventbus.Message) time.Time {
	t, err := time.Parse(time.RFC3339Nano, msg.Headers[eventbus.HeaderOccurredAt])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// unmappedEvent arma la entrada genérica de un evento sin mapeo: queda
// registrado con su subject y la causa, atribuido al tenant si el payload lo
// trae.
func unmappedEvent(msg *eventbus.Message, cause error) *mappedEvent {
	var payload struct {
		TenantID string `json:"tenant_id"`
	}
	_ = json.Unmarshal(msg.Payload, &payload)
	return &mappedEvent{RecordActionCmd: application.RecordActionCmd{
		TenantID:     payload.TenantID,
		Actor:        "system",
		Action:       domain.ActionUnmappedEvent.String(),
		ResourceType: domain.ResourceEvent.String(),
		ResourceID:   msg.ID,
		Metadata: map[string]string{
			"event_subject": msg.Subject,
			"reason":        cause.Error(),
		},
	}}
}

// ── Mapeo de eventos a acciones de auditoría ──────────────────────────────────

// eventMapping describe cómo auditar un subject: el tipo de recurso, el campo
// del payload con su ID y, si el payload declara quién actuó, ese campo.
type eventMapping struct {
	resourceType string
	idField      string
	actorField   string   // opcional
	metaFields   []string // campos del payload que se copian a metadata
}

// mappings es el catálogo de eventos auditados. La acción se deriva del
// subject sin el sufijo de versión (auth.tenant.created.v1 → auth.tenant.created).
var mappings = map[string]eventMapping{
	// ── Auth ──
	"auth.tenant.created.v1":   {"tenant", "tenant_id", "", nil},
	"auth.tenant.activated.v1": {"tenant", "tenant_id", "", nil},
	"auth.tenant.suspended.v1": {"tenant", "tenant_id", "", nil},
	"auth.user.registered.v1":  {"user", "user_id", "", nil},
	"auth.user.suspended.v1":   {"user", "user_id", "", nil},
	"auth.user.login.v1":       {"user", "user_id", "user_id", nil},
	"auth.user.logout.v1":      {"user", "user_id", "user_id", nil},
	"auth.apikey.issued.v1":    {"api_key", "api_key_id", "", nil},
	"auth.apikey.revoked.v1":   {"api_key", "api_key_id", "", nil},
	"auth.role.assigned.v1":    {"user", "user_id", "assigned_by", nil},

	// ── Ledger ──
	"ledger.account.created.v1": {"ledger_account", "account_id", "", nil},
	"ledger.entry.posted.v1":    {"journal_entry", "entry_id", "", nil},
	"ledger.entry.reversed.v1":  {"journal_entry", "reverse_entry_id", "", []string{"original_entry_id"}},

	// ── Onboarding (KYC) ──
	"onboarding.application.submitted.v1":       {"application", "application_id", "", nil},
	"onboarding.application.sent_for_review.v1": {"application", "application_id", "", nil},
	"onboarding.application.approved.v1":        {"application", "application_id", "", nil},
	"onboarding.application.rejected.v1":        {"application", "application_id", "", nil},
	"onboarding.application.more_info.v1":       {"application", "application_id", "", nil},
	"onboarding.application.reopened.v1":        {"application", "application_id", "", nil},

	// ── Payment (solo acciones sensibles) ──
	"payment.refunded.v1": {"payment", "payment_id", "", nil},

	// ── Payout ──
	"payout.initiated.v1": {"payout", "payout_id", "", nil},
	"payout.confirmed.v1": {"payout", "payout_id", "", nil},
	"payout.failed.v1":    {"payout", "payout_id", "", nil},
	"payout.unknown.v1":   {"payout", "payout_id", "", nil},

	// ── Billing ──
	"billing.plan.created.v1":     {"billing_plan", "plan_id", "", nil},
	"billing.plan.updated.v1":     {"billing_plan", "plan_id", "", nil},
	"billing.plan.deactivated.v1": {"billing_plan", "plan_id", "", nil},
	"billing.plan.assigned.v1":    {"tenant_plan", "tenant_plan_id", "", nil},

	// ── Dispute ──
	"dispute.opened.v1":   {"dispute", "dispute_id", "", nil},
	"dispute.resolved.v1": {"dispute", "dispute_id", "", nil},

	// ── Webhook (configuración) ──
	"webhook.endpoint.registered.v1":  {"webhook_endpoint", "endpoint_id", "", nil},
	"webhook.endpoint.deactivated.v1": {"webhook_endpoint", "endpoint_id", "", nil},

	// ── Compliance & AML ──
	"compliance.alert.raised.v1":            {"aml_alert", "alert_id", "", nil},
	"compliance.alert.review_proposed.v1":   {"aml_alert", "alert_id", "", nil},
	"compliance.alert.proposal_rejected.v1": {"aml_alert", "alert_id", "", nil},
	"compliance.alert.resolved.v1":          {"aml_alert", "alert_id", "", nil},
	"compliance.sanction.matched.v1":        {"aml_alert", "alert_id", "", nil},
	"compliance.sanction.cleared.v1":        {"aml_alert", "alert_id", "", nil},
	"compliance.sanction.confirmed.v1":      {"aml_alert", "alert_id", "", nil},

	// ── Reconciliation ──
	"reconciliation.run.completed.v1":        {"reconciliation_run", "run_id", "", nil},
	"reconciliation.discrepancy.resolved.v1": {"reconciliation_discrepancy", "discrepancy_id", "resolved_by", []string{"run_id", "status"}},
}

// versionSuffix es el sufijo de versión del subject (".v1", ".v2", ...).
var versionSuffix = regexp.MustCompile(`\.v[0-9]+$`)

// mappedEvent es el comando a registrar más si el actor salió del payload.
type mappedEvent struct {
	application.RecordActionCmd
	actorFromPayload bool
}

// mapEvent traduce un mensaje NATS a un RecordActionCmd.
func mapEvent(msg *eventbus.Message) (*mappedEvent, error) {
	m, ok := mappings[msg.Subject]
	if !ok {
		return nil, fmt.Errorf("no mapping for subject %q", msg.Subject)
	}

	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	str := func(field string) string {
		s, _ := payload[field].(string)
		return s
	}

	ev := &mappedEvent{RecordActionCmd: application.RecordActionCmd{
		TenantID:     str("tenant_id"),
		Actor:        "system",
		Action:       versionSuffix.ReplaceAllString(msg.Subject, ""),
		ResourceType: m.resourceType,
		ResourceID:   str(m.idField),
		Metadata:     map[string]string{"event_subject": msg.Subject},
	}}
	for _, f := range m.metaFields {
		if v := str(f); v != "" {
			ev.Metadata[f] = v
		}
	}
	if m.actorField != "" {
		if actor := str(m.actorField); actor != "" {
			ev.Actor, ev.actorFromPayload = actor, true
		}
	}
	return ev, nil
}
