package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// WebhookDelivery representa un intento de entrega de un evento a un endpoint.
//
// Ciclo de vida:
//
//	pending → delivered   (éxito en cualquier intento)
//	pending → failed      (intento fallido; hay reintento programado)
//	failed  → pending     (cuando llega la hora del reintento)
//	failed  → exhausted   (se agotaron todos los reintentos)
//
// La idempotencia se garantiza con el par (endpoint_id, event_id):
// un mismo evento solo genera una delivery por endpoint.
type WebhookDelivery struct {
	id           DeliveryID
	endpointID   EndpointID
	tenantID     TenantID
	eventType    string // normalizado sin versión, ej: "payment.captured"
	eventID      string // ID del evento de dominio original (para idempotencia)
	payload      json.RawMessage
	status       DeliveryStatus
	attemptCount int
	nextRetryAt  *time.Time
	attempts     []DeliveryAttempt
	createdAt    time.Time
	updatedAt    time.Time

	domainEvents []Event
}

// NewWebhookDelivery crea una delivery en estado pending lista para despacharse.
func NewWebhookDelivery(
	id DeliveryID,
	endpointID EndpointID,
	tenantID TenantID,
	eventType string,
	eventID string,
	payload json.RawMessage,
) *WebhookDelivery {
	now := time.Now().UTC()
	return &WebhookDelivery{
		id:           id,
		endpointID:   endpointID,
		tenantID:     tenantID,
		eventType:    stripVersion(eventType),
		eventID:      eventID,
		payload:      payload,
		status:       StatusPending,
		attemptCount: 0,
		nextRetryAt:  &now, // disponible de inmediato
		createdAt:    now,
		updatedAt:    now,
	}
}

// ReconstituteDelivery reconstruye desde el repositorio.
func ReconstituteDelivery(
	id DeliveryID, endpointID EndpointID, tenantID TenantID,
	eventType, eventID string, payload json.RawMessage,
	status DeliveryStatus, attemptCount int, nextRetryAt *time.Time,
	attempts []DeliveryAttempt, createdAt, updatedAt time.Time,
) *WebhookDelivery {
	return &WebhookDelivery{
		id: id, endpointID: endpointID, tenantID: tenantID,
		eventType: eventType, eventID: eventID, payload: payload,
		status: status, attemptCount: attemptCount, nextRetryAt: nextRetryAt,
		attempts: attempts, createdAt: createdAt, updatedAt: updatedAt,
	}
}

// RecordAttempt registra el resultado de un intento de entrega HTTP.
// Actualiza el estado según el resultado y calcula el próximo reintento.
func (d *WebhookDelivery) RecordAttempt(attempt DeliveryAttempt, now time.Time) error {
	if d.status.IsFinal() {
		return fmt.Errorf("delivery %s is already in final state %q", d.id, d.status)
	}

	d.attemptCount++
	d.attempts = append(d.attempts, attempt)
	d.updatedAt = now

	if attempt.Succeeded() {
		// Éxito: delivery completada.
		d.status = StatusDelivered
		d.nextRetryAt = nil
		return nil
	}

	// Fallo: calcular próximo reintento.
	next := NextRetryAt(d.attemptCount, now)
	if next == nil {
		// Se agotaron los reintentos.
		d.status = StatusExhausted
		d.nextRetryAt = nil
		d.record(DeliveryExhaustedEvent{
			baseEvent:  newBase(d.tenantID.String()),
			DeliveryID: d.id.String(),
			EndpointID: d.endpointID.String(),
			TenantID:   d.tenantID.String(),
			EventType_: d.eventType,
		})
	} else {
		d.status = StatusFailed
		d.nextRetryAt = next
	}

	return nil
}

// MarkReadyForRetry vuelve el estado a pending cuando llega la hora del reintento.
// Lo llama el RetryPoller antes de despachar.
func (d *WebhookDelivery) MarkReadyForRetry() error {
	if d.status != StatusFailed {
		return fmt.Errorf("delivery %s is not in failed state (is %q)", d.id, d.status)
	}
	d.status = StatusPending
	d.updatedAt = time.Now().UTC()
	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (d *WebhookDelivery) ID() DeliveryID              { return d.id }
func (d *WebhookDelivery) EndpointID() EndpointID      { return d.endpointID }
func (d *WebhookDelivery) TenantID() TenantID          { return d.tenantID }
func (d *WebhookDelivery) EventType() string           { return d.eventType }
func (d *WebhookDelivery) EventID() string             { return d.eventID }
func (d *WebhookDelivery) Payload() json.RawMessage    { return d.payload }
func (d *WebhookDelivery) Status() DeliveryStatus      { return d.status }
func (d *WebhookDelivery) AttemptCount() int           { return d.attemptCount }
func (d *WebhookDelivery) NextRetryAt() *time.Time     { return d.nextRetryAt }
func (d *WebhookDelivery) Attempts() []DeliveryAttempt { return d.attempts }
func (d *WebhookDelivery) CreatedAt() time.Time        { return d.createdAt }
func (d *WebhookDelivery) UpdatedAt() time.Time        { return d.updatedAt }

// IsDueForRetry retorna true si la delivery está pendiente de despachar ahora.
func (d *WebhookDelivery) IsDueForRetry(now time.Time) bool {
	return d.status.IsRetryable() &&
		d.nextRetryAt != nil &&
		!now.Before(*d.nextRetryAt)
}

func (d *WebhookDelivery) PullEvents() []Event {
	evs := d.domainEvents
	d.domainEvents = nil
	return evs
}

func (d *WebhookDelivery) record(ev Event) { d.domainEvents = append(d.domainEvents, ev) }
