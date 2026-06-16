package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// WebhookEnvelope es el payload que se envía al endpoint del comercio.
// Envuelve el evento original con metadatos de entrega.
type WebhookEnvelope struct {
	EventType  string          `json:"event_type"`
	EventID    string          `json:"event_id"`
	DeliveryID string          `json:"delivery_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// DispatchEventUseCase recibe un evento de dominio y crea WebhookDeliveries
// para cada endpoint activo del tenant suscrito a ese tipo de evento.
//
// El dispatch HTTP NO ocurre aquí — solo se crean las deliveries en estado
// pending. El RetryPoller (worker) las despacha de forma asíncrona.
// Esto desacopla el consumer NATS del tiempo de respuesta de los endpoints
// del comercio y garantiza que los ack a NATS sean inmediatos.
type DispatchEventUseCase struct {
	endpointRepo EndpointRepository
	deliveryRepo DeliveryRepository
}

func NewDispatchEventUseCase(
	endpointRepo EndpointRepository,
	deliveryRepo DeliveryRepository,
) *DispatchEventUseCase {
	return &DispatchEventUseCase{endpointRepo: endpointRepo, deliveryRepo: deliveryRepo}
}

func (uc *DispatchEventUseCase) Execute(ctx context.Context, event IncomingEvent) error {
	tenantID, err := domain.ParseTenantID(event.TenantID)
	if err != nil {
		return err
	}

	// Buscar endpoints activos suscritos a este tipo de evento.
	endpoints, err := uc.endpointRepo.FindActiveByTenantAndEvent(ctx, tenantID, event.EventType)
	if err != nil {
		return fmt.Errorf("dispatch event: find endpoints: %w", err)
	}
	if len(endpoints) == 0 {
		return nil // El tenant no tiene endpoints suscritos a este evento.
	}

	for _, endpoint := range endpoints {
		if err := uc.createDelivery(ctx, endpoint, event); err != nil {
			// Si falla una delivery, continuar con los otros endpoints.
			// No hay que bloquear la entrega al resto por un error de uno.
			_ = fmt.Errorf("dispatch event: endpoint %s: %w", endpoint.ID(), err)
		}
	}
	return nil
}

func (uc *DispatchEventUseCase) createDelivery(
	ctx context.Context,
	endpoint *domain.WebhookEndpoint,
	event IncomingEvent,
) error {
	// Verificar idempotencia: ¿ya existe una delivery para este (endpoint, event)?
	_, err := uc.deliveryRepo.FindByEventAndEndpoint(ctx, endpoint.ID(), event.EventID)
	if err == nil {
		return nil // ya existe, skip silencioso
	}
	if !errors.Is(err, domain.ErrDeliveryNotFound) {
		return fmt.Errorf("check idempotency: %w", err)
	}

	// Construir el envelope que recibirá el comercio.
	deliveryID := domain.NewDeliveryID()
	envelope := WebhookEnvelope{
		EventType:  event.EventType,
		EventID:    event.EventID,
		DeliveryID: deliveryID.String(),
		OccurredAt: event.OccurredAt,
		Data:       event.Data,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	delivery := domain.NewWebhookDelivery(
		deliveryID,
		endpoint.ID(),
		endpoint.TenantID(),
		event.EventType,
		event.EventID,
		payload,
	)

	if err := uc.deliveryRepo.Save(ctx, delivery); err != nil {
		return fmt.Errorf("save delivery: %w", err)
	}
	return nil
}
