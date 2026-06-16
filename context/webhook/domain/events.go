package domain

import (
	"time"

	"github.com/google/uuid"
)

type Event interface {
	EventID() string
	EventType() string
	EventTenantID() string
	OccurredAt() time.Time
}

type baseEvent struct {
	id         string
	tenantID   string
	occurredAt time.Time
}

func newBase(tenantID string) baseEvent {
	return baseEvent{id: uuid.NewString(), tenantID: tenantID, occurredAt: time.Now().UTC()}
}

func (e baseEvent) EventID() string       { return e.id }
func (e baseEvent) EventTenantID() string { return e.tenantID }
func (e baseEvent) OccurredAt() time.Time { return e.occurredAt }

// EndpointRegisteredEvent se emite al crear un nuevo endpoint.
type EndpointRegisteredEvent struct {
	baseEvent
	EndpointID string   `json:"endpoint_id"`
	TenantID   string   `json:"tenant_id"`
	URL        string   `json:"url"`
	Events     []string `json:"events"`
}

func (e EndpointRegisteredEvent) EventType() string { return "webhook.endpoint.registered.v1" }

// EndpointDeactivatedEvent se emite al desactivar un endpoint.
type EndpointDeactivatedEvent struct {
	baseEvent
	EndpointID string `json:"endpoint_id"`
	TenantID   string `json:"tenant_id"`
}

func (e EndpointDeactivatedEvent) EventType() string { return "webhook.endpoint.deactivated.v1" }

// DeliveryExhaustedEvent se emite cuando se agotan todos los reintentos.
type DeliveryExhaustedEvent struct {
	baseEvent
	DeliveryID string `json:"delivery_id"`
	EndpointID string `json:"endpoint_id"`
	TenantID   string `json:"tenant_id"`
	EventType_ string `json:"event_type"`
}

func (e DeliveryExhaustedEvent) EventType() string { return "webhook.delivery.exhausted.v1" }
