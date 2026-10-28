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
	// Update persiste la transición con concurrencia optimista: solo aplica si
	// la fila sigue en `from`. Si otra revisión ya la movió retorna
	// domain.ErrAlertNotOpen.
	Update(ctx context.Context, a *domain.Alert, from domain.AlertStatus) error
	FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error)
	// List lista alertas de la plataforma (cross-tenant) según el filtro.
	List(ctx context.Context, filter AlertFilter, limit int) ([]*domain.Alert, error)
}

// AlertFilter acota un listado de alertas. Campos vacíos = sin filtro.
type AlertFilter struct {
	TenantID domain.TenantID
	Status   domain.AlertStatus
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
