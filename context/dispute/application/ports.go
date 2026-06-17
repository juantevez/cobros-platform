package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// DisputeRepository persiste y recupera Disputes con su Evidence.
type DisputeRepository interface {
	Save(ctx context.Context, d *domain.Dispute) error
	Update(ctx context.Context, d *domain.Dispute) error
	FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error)
	FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Dispute, error)
	// ListOverdue retorna disputes abiertas cuyo deadline ya pasó.
	// Usado por el ExpiryPoller del worker.
	ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el tiempo.
type Clock interface {
	Now() time.Time
}
