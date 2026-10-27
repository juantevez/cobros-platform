package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type PayoutID string
type TenantID string

func NewPayoutID() PayoutID { return PayoutID(uuid.NewString()) }

func ParsePayoutID(s string) (PayoutID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid payout id: %w", err)
	}
	return PayoutID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id PayoutID) String() string { return string(id) }
func (id TenantID) String() string { return string(id) }

// ── PayoutStatus ──────────────────────────────────────────────────────────────

type PayoutStatus string

const (
	// StatusInitiated: calculado y registrado en Ledger. Listo para enviar.
	StatusInitiated PayoutStatus = "initiated"
	// StatusProcessing: transferencia enviada al banco, esperando confirmación.
	StatusProcessing PayoutStatus = "processing"
	// StatusConfirmed: el banco confirmó la acreditación. Estado final exitoso.
	StatusConfirmed PayoutStatus = "confirmed"
	// StatusFailed: la transferencia falló (cuenta inválida, rechazo bancario).
	// El asiento en Ledger se revierte automáticamente.
	StatusFailed PayoutStatus = "failed"
	// StatusUnknown: el banco no respondió (timeout / error de red). NO es un
	// estado final: los fondos siguen en payout_transit y el dinero puede haber
	// sido acreditado o no. Debe resolverse por reconciliación antes de cerrar.
	StatusUnknown PayoutStatus = "unknown"
)

func (s PayoutStatus) String() string { return string(s) }

// IsFinal indica que el payout no admite más transiciones.
// StatusUnknown NO es final: requiere resolución (§10.3).
func (s PayoutStatus) IsFinal() bool {
	return s == StatusConfirmed || s == StatusFailed
}

// ── Money ─────────────────────────────────────────────────────────────────────

// Money representa un monto en unidades mínimas (centavos). Nunca float.
type Money struct {
	amount   int64
	currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount <= 0 {
		return Money{}, ErrInvalidAmount
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{amount: amount, currency: cur}, nil
}

func ReconstituteMoney(amount int64, currency string) Money {
	return Money{amount: amount, currency: currency}
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) String() string   { return fmt.Sprintf("%d %s", m.amount, m.currency) }

// ── BankAccountInfo ───────────────────────────────────────────────────────────

// BankAccountInfo contiene los datos de la cuenta bancaria destino del payout.
// Se obtiene del contexto Onboarding al momento de calcular el payout.
//
// El número de cuenta es dato sensible: se guarda sin enmascarar (hace falta
// para transferir y para el snapshot de auditoría) pero sólo puede leerse vía
// AccountNumber() —para el adaptador bancario y la persistencia— o vía
// MaskedAccountNumber() —para respuestas HTTP y logs—.
type BankAccountInfo struct {
	AccountType   string // "CBU", "CVU", "IBAN"
	accountNumber string
	BankName      string
	HolderName    string
	Currency      string
}

// NewBankAccountInfo construye y valida la info de la cuenta destino.
// Un número vacío significa que no hay cuenta utilizable: ErrNoBankAccount.
func NewBankAccountInfo(accountType, accountNumber, bankName, holderName, currency string) (BankAccountInfo, error) {
	num := strings.TrimSpace(accountNumber)
	if num == "" {
		return BankAccountInfo{}, ErrNoBankAccount
	}
	if len(num) > 64 {
		return BankAccountInfo{}, fmt.Errorf("%w: account number too long", ErrNoBankAccount)
	}
	return BankAccountInfo{
		AccountType:   strings.ToUpper(strings.TrimSpace(accountType)),
		accountNumber: num,
		BankName:      bankName,
		HolderName:    holderName,
		Currency:      currency,
	}, nil
}

// AccountNumber retorna el número completo. Usar únicamente en el camino
// outbound (transferencia bancaria y persistencia del snapshot).
func (b BankAccountInfo) AccountNumber() string { return b.accountNumber }

// MustBankAccountInfo reconstruye un BankAccountInfo desde datos ya
// persistidos (reconstitución), sin revalidar. Es responsabilidad del
// adaptador de persistencia garantizar la integridad del dato.
func MustBankAccountInfo(accountType, accountNumber, bankName, holderName, currency string) BankAccountInfo {
	return BankAccountInfo{
		AccountType:   accountType,
		accountNumber: accountNumber,
		BankName:      bankName,
		HolderName:    holderName,
		Currency:      currency,
	}
}

// MaskedAccountNumber oculta todos menos los últimos 4 caracteres
// (ej. "••••••••••••••••7370"). Es la única forma permitida de exponer la
// cuenta en APIs de lectura o logs.
func (b BankAccountInfo) MaskedAccountNumber() string {
	const visible = 4
	r := []rune(b.accountNumber)
	if len(r) <= visible {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-visible) + string(r[len(r)-visible:])
}
