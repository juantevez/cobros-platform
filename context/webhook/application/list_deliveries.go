package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

const defaultDeliveryLimit = 50

// ListDeliveriesUseCase lista las deliveries de un tenant.
type ListDeliveriesUseCase struct {
	deliveryRepo DeliveryRepository
}

func NewListDeliveriesUseCase(repo DeliveryRepository) *ListDeliveriesUseCase {
	return &ListDeliveriesUseCase{deliveryRepo: repo}
}

func (uc *ListDeliveriesUseCase) Execute(ctx context.Context, q ListDeliveriesQuery) ([]DeliveryView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultDeliveryLimit
	}

	deliveries, err := uc.deliveryRepo.ListByTenant(ctx, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}

	views := make([]DeliveryView, len(deliveries))
	for i, d := range deliveries {
		views[i] = toDeliveryView(d)
	}
	return views, nil
}

// GetDeliveryUseCase retorna una delivery con todos sus intentos.
type GetDeliveryUseCase struct {
	deliveryRepo DeliveryRepository
}

func NewGetDeliveryUseCase(repo DeliveryRepository) *GetDeliveryUseCase {
	return &GetDeliveryUseCase{deliveryRepo: repo}
}

func (uc *GetDeliveryUseCase) Execute(ctx context.Context, q GetDeliveryQuery) (DeliveryView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return DeliveryView{}, err
	}
	deliveryID, err := domain.ParseDeliveryID(q.DeliveryID)
	if err != nil {
		return DeliveryView{}, err
	}

	d, err := uc.deliveryRepo.FindByID(ctx, deliveryID)
	if err != nil {
		return DeliveryView{}, err
	}
	if d.TenantID() != tenantID {
		return DeliveryView{}, domain.ErrDeliveryNotFound
	}
	return toDeliveryView(d), nil
}

// ListEndpointsUseCase lista los endpoints del tenant.
type ListEndpointsUseCase struct {
	endpointRepo EndpointRepository
}

func NewListEndpointsUseCase(repo EndpointRepository) *ListEndpointsUseCase {
	return &ListEndpointsUseCase{endpointRepo: repo}
}

func (uc *ListEndpointsUseCase) Execute(ctx context.Context, tenantID string) ([]EndpointView, error) {
	tid, err := domain.ParseTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	endpoints, err := uc.endpointRepo.FindByTenant(ctx, tid)
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	views := make([]EndpointView, len(endpoints))
	for i, e := range endpoints {
		views[i] = EndpointView{
			ID:          e.ID().String(),
			TenantID:    e.TenantID().String(),
			URL:         e.URL(),
			SecretHint:  e.SecretHint(),
			Events:      e.Events(),
			Active:      e.Active(),
			Description: e.Description(),
			CreatedAt:   e.CreatedAt().Format(time.RFC3339),
		}
	}
	return views, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toDeliveryView(d *domain.WebhookDelivery) DeliveryView {
	attempts := make([]AttemptView, len(d.Attempts()))
	for i, a := range d.Attempts() {
		attempts[i] = AttemptView{
			AttemptNum:   a.AttemptNum(),
			HTTPStatus:   a.HTTPStatus(),
			ResponseBody: a.ResponseBody(),
			Error:        a.ErrMsg(),
			DurationMs:   a.DurationMs(),
			AttemptedAt:  a.AttemptedAt().Format(time.RFC3339),
		}
	}
	return DeliveryView{
		ID:           d.ID().String(),
		EndpointID:   d.EndpointID().String(),
		EventType:    d.EventType(),
		EventID:      d.EventID(),
		Status:       d.Status().String(),
		AttemptCount: d.AttemptCount(),
		NextRetryAt:  d.NextRetryAt(),
		Attempts:     attempts,
		CreatedAt:    d.CreatedAt().Format(time.RFC3339),
		UpdatedAt:    d.UpdatedAt().Format(time.RFC3339),
	}
}
