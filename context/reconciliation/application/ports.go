package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// RunRepository persiste y recupera ReconciliationRuns.
type RunRepository interface {
	Save(ctx context.Context, r *domain.ReconciliationRun) error
	Update(ctx context.Context, r *domain.ReconciliationRun) error
	FindByID(ctx context.Context, id domain.RunID) (*domain.ReconciliationRun, error)
	List(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.ReconciliationRun, error)
}

// DiscrepancyRepository persiste y recupera Discrepancies.
type DiscrepancyRepository interface {
	SaveAll(ctx context.Context, discrepancies []*domain.Discrepancy) error
	Update(ctx context.Context, d *domain.Discrepancy) error
	FindByID(ctx context.Context, id domain.DiscrepancyID) (*domain.Discrepancy, error)
	ListByRun(ctx context.Context, runID domain.RunID, statusFilter string, limit int) ([]*domain.Discrepancy, error)
}

// SystemPayment es la vista de un pago leída directamente de la tabla payments.
type SystemPayment struct {
	PaymentID    string
	PSPReference string
	Amount       int64
	Currency     string
	Status       string     // "captured", "failed", etc.
	CapturedAt   *time.Time
}

// PaymentReader lee pagos del sistema para un período dado.
// Accede directamente a la tabla payments (misma BD, mismo esquema).
type PaymentReader interface {
	ReadByPeriod(ctx context.Context, tenantID domain.TenantID, from, to time.Time) ([]SystemPayment, error)
}

// PSPRecord es un registro del informe del PSP (CSV u otro formato).
type PSPRecord struct {
	TransactionID string // equivale al psp_reference del sistema
	Amount        int64  // en centavos
	Currency      string
	Status        string    // "captured", "rejected", "refunded"
	ProcessedAt   time.Time
}

// ReportParser parsea el informe del PSP (CSV) y retorna los registros.
type ReportParser interface {
	Parse(data []byte) ([]PSPRecord, error)
}

// LedgerChecker verifica la coherencia interna del Ledger.
type LedgerChecker interface {
	// CheckBalance verifica que sum(débitos) == sum(créditos) para el período.
	// Retorna el imbalance en centavos (0 = todo balanceado).
	CheckBalance(ctx context.Context, tenantID domain.TenantID, from, to time.Time) (int64, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}
