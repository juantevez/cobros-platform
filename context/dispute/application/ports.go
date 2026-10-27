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
	// Update aplica la mutación con concurrencia optimista: el UPDATE filtra por
	// la versión leída (d.Version()-1) y retorna domain.ErrOptimisticLock si
	// otra transición ganó la carrera (0 filas afectadas).
	Update(ctx context.Context, d *domain.Dispute) error
	FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error)
	FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Dispute, error)
	// ListOverdue retorna disputes abiertas cuyo deadline ya pasó.
	// Usado por el ExpiryPoller del worker.
	ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error)
	// ListAll retorna disputas de TODOS los tenants (vista operatoria global del
	// back-office; corre sin tenant en contexto para no filtrarse por RLS).
	ListAll(ctx context.Context, statusFilter string, limit int) ([]*domain.Dispute, error)
}

// PaymentReader lee el pago disputado desde la tabla `payments` del BC Payment
// (mismo patrón cross-context que payment/tenant.StatusGate o payout/ledger).
// Permite validar en OpenDispute: existencia, estado "captured", titularidad,
// monto máximo disputable y moneda.
type PaymentReader interface {
	FindPayment(ctx context.Context, paymentID string) (domain.PaymentRef, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el tiempo.
type Clock interface {
	Now() time.Time
}
