package domain

import "time"

// Account es una cuenta contable dentro del libro mayor.
//
// Cada tenant tiene cuentas propias para cada tipo (merchant_balance,
// platform_fees, etc.) y moneda. Las cuentas son de solo creación:
// no se eliminan ni se modifican, solo acumulan movimientos vía postings.
//
// El saldo de una cuenta se mantiene en la tabla account_balances,
// actualizado transaccionalmente con cada JournalEntry que la afecta.
type Account struct {
	id          AccountID
	tenantID    TenantID
	accountType AccountType
	currency    string // ISO 4217
	description string
	createdAt   time.Time

	events []Event
}

// NewAccount crea una nueva cuenta contable.
// Emite AccountCreatedEvent.
func NewAccount(id AccountID, tenantID TenantID, accountType AccountType, currency string, description string) (*Account, error) {
	// Validar currency reutilizando Money
	if _, err := NewMoney(0, currency); err != nil {
		return nil, err
	}

	a := &Account{
		id:          id,
		tenantID:    tenantID,
		accountType: accountType,
		currency:    currency,
		description: description,
		createdAt:   time.Now().UTC(),
	}

	a.record(AccountCreatedEvent{
		baseEvent:   newBase(tenantID.String()),
		AccountID:   id.String(),
		TenantID:    tenantID.String(),
		AccountType: accountType.String(),
		Currency:    currency,
	})

	return a, nil
}

// ReconstituteAccount reconstruye una Account desde el repositorio.
func ReconstituteAccount(id AccountID, tenantID TenantID, accountType AccountType, currency, description string, createdAt time.Time) *Account {
	return &Account{
		id:          id,
		tenantID:    tenantID,
		accountType: accountType,
		currency:    currency,
		description: description,
		createdAt:   createdAt,
	}
}

func (a *Account) ID() AccountID           { return a.id }
func (a *Account) TenantID() TenantID      { return a.tenantID }
func (a *Account) AccountType() AccountType { return a.accountType }
func (a *Account) Currency() string        { return a.currency }
func (a *Account) Description() string     { return a.description }
func (a *Account) CreatedAt() time.Time    { return a.createdAt }

func (a *Account) PullEvents() []Event {
	evs := a.events
	a.events = nil
	return evs
}

func (a *Account) record(e Event) { a.events = append(a.events, e) }
