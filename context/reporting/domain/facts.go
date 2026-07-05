// Package domain define el read-model del contexto de Reporting.
//
// A diferencia de los contextos de escritura, Reporting no tiene agregados ni
// invariantes de negocio: es un modelo de lectura (CQRS) construido a partir de
// los eventos de dominio de otros contextos. Sus "hechos" son inmutables y las
// métricas se agregan en tiempo de consulta.
package domain

import "time"

// PaymentFact es un hecho inmutable proyectado desde payment.captured.v1.
// Una fila por pago; la clave natural es PaymentID (garantiza idempotencia).
type PaymentFact struct {
	PaymentID     string
	TenantID      string
	Currency      string
	Amount        int64 // bruto en centavos
	PlatformFee   int64 // comisión de la plataforma en centavos
	PSPFee        int64 // comisión del PSP en centavos
	PaymentMethod string
	CapturedAt    time.Time
}

// LedgerMovement es un hecho inmutable proyectado desde ledger.entry.posted.v1.
// Una fila por posting; la clave natural es (EntryID, AccountID, Direction).
type LedgerMovement struct {
	EntryID     string
	AccountID   string
	Direction   string // "debit" | "credit"
	TenantID    string
	AccountType string
	Currency    string
	Amount      int64 // centavos, siempre > 0
	PostedAt    time.Time
}
