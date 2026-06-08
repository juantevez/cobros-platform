package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// TxManager abstrae las transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// AccountRepository persiste y recupera cuentas contables.
type AccountRepository interface {
	Save(ctx context.Context, a *domain.Account) error
	FindByID(ctx context.Context, id domain.AccountID) (*domain.Account, error)
	FindByTenantAndType(ctx context.Context, tenantID domain.TenantID, accountType domain.AccountType, currency string) (*domain.Account, error)
}

// EntryRepository persiste y recupera asientos contables.
// Los postings se guardan y cargan como parte del Entry (no hay PostingRepository).
type EntryRepository interface {
	Save(ctx context.Context, e *domain.JournalEntry) error
	FindByID(ctx context.Context, id domain.EntryID) (*domain.JournalEntry, error)
	// FindByIdempotencyKey busca por clave de idempotencia dentro del tenant.
	FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.JournalEntry, error)
}

// BalanceRepository mantiene los saldos de cada cuenta actualizados.
// Se invoca dentro de la misma transacción que EntryRepository.Save.
type BalanceRepository interface {
	// Apply actualiza el saldo de cada cuenta según los postings del entry.
	// Crédito suma al saldo, débito lo resta.
	Apply(ctx context.Context, postings []domain.Posting) error
	// GetBalance retorna el saldo actual de una cuenta.
	GetBalance(ctx context.Context, accountID domain.AccountID) (int64, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox transaccional.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// Clock abstrae el acceso al tiempo.
type Clock interface {
	Now() time.Time
}
