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
	ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter domain.AlertStatus, limit int) ([]*domain.Alert, error)
}

// WatchlistRepository consulta y gestiona la lista de vigilancia global.
type WatchlistRepository interface {
	// Screen retorna las entradas vigentes (no dadas de baja) que coinciden
	// por tokens con el nombre normalizado dado, puntuadas con
	// domain.MatchScore. Vacío = sin coincidencias.
	Screen(ctx context.Context, normalizedName string) ([]domain.Match, error)
	Add(ctx context.Context, entry domain.WatchlistEntry, normalizedName string, addedAt time.Time) error
	// Remove da de baja (soft delete) una entrada vigente. Si no existe o ya
	// estaba dada de baja retorna domain.ErrWatchlistEntryNotFound.
	Remove(ctx context.Context, id string, removedAt time.Time) error
	List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error)
}

// MonitoredPayment es la proyección propia de un pago capturado, construida a
// partir de payment.captured.v1 (Compliance no lee el esquema de Payment).
type MonitoredPayment struct {
	PaymentID  string
	TenantID   string
	Amount     int64
	Currency   string
	CapturedAt time.Time
}

// TransactionLog es la proyección de pagos capturados que alimenta las reglas
// de ventana (velocity y volumen).
type TransactionLog interface {
	// Record registra el pago; idempotente por payment_id (re-entregas no duplican).
	Record(ctx context.Context, p MonitoredPayment) error
	// CountSince cuenta los pagos del tenant registrados desde `since`.
	CountSince(ctx context.Context, tenantID string, since time.Time) (int, error)
	// SumAmountSince suma los montos del tenant en `currency` desde `since`.
	SumAmountSince(ctx context.Context, tenantID, currency string, since time.Time) (int64, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el tiempo.
type Clock interface {
	Now() time.Time
}
