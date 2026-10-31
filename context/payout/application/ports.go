package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// PayoutRepository persiste y recupera el agregado Payout.
type PayoutRepository interface {
	// Save inserta el payout. Si ya existe uno del tenant con la misma
	// clave de idempotencia, retorna domain.ErrPayoutAlreadyExists
	// (mapeado desde la violación del índice UNIQUE).
	Save(ctx context.Context, p *domain.Payout) error
	Update(ctx context.Context, p *domain.Payout) error
	FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error)
	// FindByIdempotencyKey busca un payout existente por (tenant, key).
	// Retorna domain.ErrPayoutNotFound si no existe.
	FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payout, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Payout, error)
}

// BalanceChecker consulta el saldo disponible de un tenant en el Ledger.
// En un monolith, la implementación hace una query directa a account_balances.
// En un futuro microservicio, sería una llamada HTTP o un evento de consulta.
//
// El método se ejecuta SIEMPRE dentro de RunInTx: la implementación real fija
// el estado contable con pg_advisory_xact_lock por (tenant, currency), cerrando
// la ventana TOCTOU entre leer el saldo y reservar los fondos con el INSERT
// del payout (mismas claves de lock → serialización entre requests concurrentes).
type BalanceChecker interface {
	GetAvailableBalance(ctx context.Context, tenantID domain.TenantID, currency string) (int64, error)
}

// BankAccountProvider obtiene los datos de la cuenta bancaria del comercio
// registrada durante el Onboarding. Sólo debe retornar cuentas verificadas
// (verified = true) de solicitudes aprobadas; verificarla antes de desembolsar
// es requisito regulatorio mínimo.
type BankAccountProvider interface {
	GetBankAccount(ctx context.Context, tenantID domain.TenantID) (domain.BankAccountInfo, error)
}

// PayoutGate verifica que el comercio pueda desembolsar: tenant activo y sin
// hold preventivo de cumplimiento. Retorna domain.ErrPayoutsNotAllowed si no.
type PayoutGate interface {
	EnsurePayoutsAllowed(ctx context.Context, tenantID domain.TenantID) error
}

// BankTransferAdapter abstrae la ejecución de transferencias bancarias.
// Un rechazo definitivo del banco debe envolver ErrBankRejected; cualquier
// otro error se trata como resultado desconocido.
// En Fase 3: implementación Mock.
// En Fase 4: se conecta con el banco real, MP Transfers, o Prex.
type BankTransferAdapter interface {
	Transfer(ctx context.Context, req TransferRequest) (TransferResult, error)
	Name() string
}

// TransferRequest contiene los datos para ejecutar una transferencia.
type TransferRequest struct {
	PayoutID       string
	IdempotencyKey string
	Amount         int64
	Currency       string
	AccountType    string // CBU, CVU, IBAN
	AccountNumber  string
	BankName       string
	HolderName     string
	Description    string
}

// TransferResult es la respuesta del adaptador de transferencia.
type TransferResult struct {
	BankReference string // referencia asignada por el banco
	Status        string // "confirmed" | "pending" | "failed"
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// BalanceLocker es un puerto opcional que implementan los BalanceChecker
// respaldados por Postgres. LockBalance toma un pg_advisory_xact_lock keyed
// por (tenant, currency) que se libera con el commit/rollback de la tx activa.
// El caso de uso lo invoca —si está disponible— antes de leer el saldo, para
// serializar payouts concurrentes y cerrar la ventana TOCTOU (§10.4).
type BalanceLocker interface {
	LockBalance(ctx context.Context, tenantID domain.TenantID, currency string) error
}
