package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// EndpointRepository persiste y recupera WebhookEndpoints.
type EndpointRepository interface {
	Save(ctx context.Context, e *domain.WebhookEndpoint) error
	Update(ctx context.Context, e *domain.WebhookEndpoint) error
	FindByID(ctx context.Context, id domain.EndpointID) (*domain.WebhookEndpoint, error)
	FindByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.WebhookEndpoint, error)
	// FindActiveByTenantAndEvent retorna todos los endpoints activos del tenant
	// suscritos al eventType dado. Usado por el consumer NATS.
	FindActiveByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) ([]*domain.WebhookEndpoint, error)
}

// DeliveryRepository persiste y recupera WebhookDeliveries.
type DeliveryRepository interface {
	Save(ctx context.Context, d *domain.WebhookDelivery) error
	Update(ctx context.Context, d *domain.WebhookDelivery) error
	FindByID(ctx context.Context, id domain.DeliveryID) (*domain.WebhookDelivery, error)
	// FindByEventAndEndpoint verifica idempotencia.
	FindByEventAndEndpoint(ctx context.Context, endpointID domain.EndpointID, eventID string) (*domain.WebhookDelivery, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.WebhookDelivery, error)
	// ListDueForRetry retorna deliveries pending/failed con nextRetryAt <= now.
	// Usado por el RetryPoller del worker.
	ListDueForRetry(ctx context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error)
}

// HTTPDispatcher ejecuta la llamada HTTP hacia el endpoint del comercio.
type HTTPDispatcher interface {
	Dispatch(ctx context.Context, endpoint *domain.WebhookEndpoint, delivery *domain.WebhookDelivery) (domain.DeliveryAttempt, error)
}

// SecretGenerator genera el secret HMAC para nuevos endpoints.
type SecretGenerator interface {
	Generate() (string, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el acceso al tiempo.
type Clock interface {
	Now() time.Time
}

// ── IncomingEvent ─────────────────────────────────────────────────────────────

// IncomingEvent es el evento de dominio recibido desde otro contexto via NATS.
// El consumer lo construye a partir del mensaje NATS antes de llamar a DispatchEvent.
type IncomingEvent struct {
	TenantID   string
	EventType  string          // ej: "payment.captured.v1"
	EventID    string          // ID único del evento original
	OccurredAt time.Time
	Data       json.RawMessage // payload original del evento
}
