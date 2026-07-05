package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// AlertRepository persiste y recupera alertas de compliance.
type AlertRepository interface {
	// Save inserta una alerta. Si viola la unicidad (tenant, tipo, subject)
	// retorna domain.ErrDuplicateAlert (para idempotencia ante re-entregas).
	Save(ctx context.Context, a *domain.Alert) error
	Update(ctx context.Context, a *domain.Alert) error
	FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Alert, error)
}

// WatchlistRepository consulta y gestiona la lista de vigilancia global.
type WatchlistRepository interface {
	// Screen retorna las entradas cuya forma normalizada aparece en el nombre
	// normalizado dado (containment). Vacío = sin coincidencias.
	Screen(ctx context.Context, normalizedName string) ([]domain.Match, error)
	Add(ctx context.Context, entry domain.WatchlistEntry, normalizedName string, addedAt time.Time) error
	List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error)
}

// TransactionReader lee el historial de pagos para las reglas de velocity.
// Consulta directamente la tabla payments (misma BD, patrón de lectura cruzada).
type TransactionReader interface {
	// CountCapturedSince cuenta los pagos capturados del tenant desde `since`.
	CountCapturedSince(ctx context.Context, tenantID string, since time.Time) (int, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el tiempo.
type Clock interface {
	Now() time.Time
}
