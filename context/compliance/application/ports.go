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
	// SaveUnlessRecent inserta una alerta de ventana (velocity/volumen) salvo
	// que el tenant ya tenga otra del mismo tipo cuyo subject empiece con
	// subjectPrefix y creada desde `since` (cooldown). En ese caso retorna
	// domain.ErrDuplicateAlert. Debe correr dentro de RunInTx: serializa por
	// (tenant, prefijo) para que pagos concurrentes no dupliquen la alerta.
	SaveUnlessRecent(ctx context.Context, a *domain.Alert, subjectPrefix string, since time.Time) error
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
// de ventana (velocity y volumen). Las ventanas son deslizantes sobre la hora
// del evento: (since, until].
type TransactionLog interface {
	// Record registra el pago; idempotente por payment_id (re-entregas no duplican).
	Record(ctx context.Context, p MonitoredPayment) error
	// CountInWindow cuenta los pagos del tenant con since < captured_at <= until.
	CountInWindow(ctx context.Context, tenantID string, since, until time.Time) (int, error)
	// SumAmountInWindow suma los montos del tenant en `currency` en la ventana.
	SumAmountInWindow(ctx context.Context, tenantID, currency string, since, until time.Time) (int64, error)
	// PurgeBefore borra hasta `limit` pagos (de todos los tenants) con
	// captured_at < before y retorna cuántos borró.
	PurgeBefore(ctx context.Context, before time.Time, limit int) (int, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el tiempo.
type Clock interface {
	Now() time.Time
}
