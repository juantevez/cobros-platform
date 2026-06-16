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
	Save(ctx context.Context, p *domain.Payout) error
	Update(ctx context.Context, p *domain.Payout) error
	FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Payout, error)
}

// BalanceChecker consulta el saldo disponible de un tenant en el Ledger.
// En un monolith, la implementación hace una query directa a account_balances.
// En un futuro microservicio, sería una llamada HTTP o un evento de consulta.
type BalanceChecker interface {
	GetAvailableBalance(ctx context.Context, tenantID domain.TenantID, currency string) (int64, error)
}

// BankAccountProvider obtiene los datos de la cuenta bancaria del comercio
// registrada durante el Onboarding.
type BankAccountProvider interface {
	GetBankAccount(ctx context.Context, tenantID domain.TenantID) (domain.BankAccountInfo, error)
}

// BankTransferAdapter abstrae la ejecución de transferencias bancarias.
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
