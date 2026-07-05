package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

// ProjectionWriter persiste los hechos inmutables del read-model.
// Las implementaciones DEBEN ser idempotentes: proyectar dos veces el mismo
// evento (re-entrega de JetStream) no debe alterar el resultado.
type ProjectionWriter interface {
	SavePaymentFact(ctx context.Context, f domain.PaymentFact) error
	SaveLedgerMovement(ctx context.Context, m domain.LedgerMovement) error
}

// AccountTypeReader resuelve el tipo de una cuenta contable a partir de su ID.
// Los eventos ledger.entry.posted.v1 traen el account_id pero no el tipo; se
// consulta la tabla ledger_accounts (misma BD) para denormalizarlo al proyectar.
type AccountTypeReader interface {
	AccountType(ctx context.Context, accountID string) (string, error)
}

// ReportReader ejecuta las consultas agregadas del dashboard.
// Todas las consultas se filtran por tenant (aislamiento por comercio).
type ReportReader interface {
	Volume(ctx context.Context, q VolumeQuery) ([]domain.VolumePoint, error)
	Revenue(ctx context.Context, q RevenueQuery) ([]domain.RevenueSummary, error)
	Balances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error)
}

// VolumeQuery describe una consulta de serie de volumen transaccional.
type VolumeQuery struct {
	TenantID    string
	From        time.Time
	To          time.Time
	Granularity domain.Granularity
}

// RevenueQuery describe una consulta de resumen de revenue por período.
type RevenueQuery struct {
	TenantID string
	From     time.Time
	To       time.Time
}
