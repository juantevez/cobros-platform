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
	// Update persiste el run solo si en la base sigue en la versión leída
	// (optimistic locking) y la incrementa; si otro proceso lo cambió antes,
	// devuelve domain.ErrRunAlreadyRunning.
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
	ProcessedAt   *time.Time // nil si el informe no lo trae
}

// ReportParser parsea el informe del PSP (CSV) y retorna los registros.
type ReportParser interface {
	Parse(data []byte) ([]PSPRecord, error)
}

// LedgerImbalance es un asiento que no balancea en una moneda: créditos menos
// débitos ≠ 0. Un asiento sin postings se informa con Currency vacío.
type LedgerImbalance struct {
	EntryID   string
	Currency  string
	Imbalance int64 // créditos - débitos, en centavos
}

// LedgerCheck es el resultado de revisar el Ledger de un período.
type LedgerCheck struct {
	EntriesChecked  int               // asientos creados en el período
	Unbalanced      []LedgerImbalance // ordenados por asiento y moneda
	CapturesChecked int               // pagos capturados en el período
	MissingEntries  []SystemPayment   // capturas sin su asiento payment_captured_<id>
}

// LedgerChecker verifica la coherencia interna del Ledger.
type LedgerChecker interface {
	// Check revisa el período: cada asiento debe balancear por moneda y cada
	// pago capturado (captured o refunded) debe tener su asiento de captura.
	Check(ctx context.Context, tenantID domain.TenantID, from, to time.Time) (LedgerCheck, error)
}

// Clock da la hora actual (inyectable en tests).
type Clock interface {
	Now() time.Time
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}
