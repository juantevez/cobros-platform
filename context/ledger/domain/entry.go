package domain

import (
	"fmt"
	"time"
)

// Posting es una línea de un asiento contable.
// Siempre vive dentro de un JournalEntry; no tiene vida propia.
//
// Invariante: Amount > 0. El sentido del movimiento lo da Direction.
type Posting struct {
	id        PostingID
	accountID AccountID
	direction Direction
	money     Money
}

func newPosting(accountID AccountID, direction Direction, money Money) (Posting, error) {
	if money.IsZero() {
		return Posting{}, ErrZeroAmount
	}
	return Posting{
		id:        NewPostingID(),
		accountID: accountID,
		direction: direction,
		money:     money,
	}, nil
}

func ReconstitutePosting(id PostingID, accountID AccountID, direction Direction, money Money) Posting {
	return Posting{id: id, accountID: accountID, direction: direction, money: money}
}

func (p Posting) ID() PostingID        { return p.id }
func (p Posting) AccountID() AccountID { return p.accountID }
func (p Posting) Direction() Direction { return p.direction }
func (p Posting) Money() Money         { return p.money }
func (p Posting) IsDebit() bool        { return p.direction == DirectionDebit }
func (p Posting) IsCredit() bool       { return p.direction == DirectionCredit }

// ── JournalEntry ──────────────────────────────────────────────────────────────

// JournalEntry es el agregado raíz del Ledger.
//
// Representa un asiento contable completo: un conjunto de postings que
// expresan un hecho económico. Es inmutable una vez creado y confirmado.
//
// Invariantes que garantiza este agregado:
//  1. Tiene al menos 2 postings.
//  2. sum(débitos) == sum(créditos) en la misma moneda (doble partida).
//  3. Todos los postings tienen la misma moneda.
//  4. Todos los amounts son > 0.
//
// La idempotencia (un solo asiento por idempotency_key) se garantiza
// con un UNIQUE constraint en la base de datos y verificación en el caso de uso.
type JournalEntry struct {
	id             EntryID
	tenantID       TenantID
	idempotencyKey string
	description    string
	metadata       map[string]string
	postings       []Posting
	occurredAt     time.Time
	createdAt      time.Time

	events []Event
}

// PostingInput es el input para construir un posting al crear un JournalEntry.
type PostingInput struct {
	AccountID AccountID
	Direction Direction
	Amount    int64
	Currency  string
}

// NewJournalEntry crea y valida un JournalEntry completo.
// Verifica doble partida, moneda única y mínimo de 2 postings.
func NewJournalEntry(
	id EntryID,
	tenantID TenantID,
	idempotencyKey string,
	description string,
	metadata map[string]string,
	occurredAt time.Time,
	inputs []PostingInput,
) (*JournalEntry, error) {

	if len(inputs) < 2 {
		return nil, ErrNotEnoughPostings
	}

	// Construir los postings y verificar moneda única.
	postings := make([]Posting, 0, len(inputs))
	var currency string

	for i, inp := range inputs {
		money, err := NewMoney(inp.Amount, inp.Currency)
		if err != nil {
			return nil, fmt.Errorf("posting %d: %w", i, err)
		}
		if i == 0 {
			currency = money.Currency()
		} else if money.Currency() != currency {
			return nil, ErrCurrencyMismatch
		}

		p, err := newPosting(inp.AccountID, inp.Direction, money)
		if err != nil {
			return nil, fmt.Errorf("posting %d: %w", i, err)
		}
		postings = append(postings, p)
	}

	// Verificar invariante de doble partida.
	if err := validateBalance(postings); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	e := &JournalEntry{
		id:             id,
		tenantID:       tenantID,
		idempotencyKey: idempotencyKey,
		description:    description,
		metadata:       metadata,
		postings:       postings,
		occurredAt:     occurredAt,
		createdAt:      now,
	}

	// Construir el evento con los postings serializables.
	postingEvents := make([]PostingEvent, len(postings))
	for i, p := range postings {
		postingEvents[i] = PostingEvent{
			AccountID: p.AccountID().String(),
			Direction: p.Direction().String(),
			Amount:    p.Money().Amount(),
			Currency:  p.Money().Currency(),
		}
	}

	e.record(EntryPostedEvent{
		baseEvent:      newBase(tenantID.String()),
		EntryID:        id.String(),
		TenantID:       tenantID.String(),
		IdempotencyKey: idempotencyKey,
		Description:    description,
		Postings:       postingEvents,
	})

	return e, nil
}

// ReconstituteJournalEntry reconstruye un JournalEntry desde el repositorio.
// No valida ni emite eventos.
func ReconstituteJournalEntry(
	id EntryID,
	tenantID TenantID,
	idempotencyKey, description string,
	metadata map[string]string,
	postings []Posting,
	occurredAt, createdAt time.Time,
) *JournalEntry {
	return &JournalEntry{
		id:             id,
		tenantID:       tenantID,
		idempotencyKey: idempotencyKey,
		description:    description,
		metadata:       metadata,
		postings:       postings,
		occurredAt:     occurredAt,
		createdAt:      createdAt,
	}
}

// BuildReverse construye un JournalEntry que anula contablemente este entry.
// Invierte débitos↔créditos de cada posting con el mismo monto.
func (e *JournalEntry) BuildReverse(reverseID EntryID, reverseIdempotencyKey string) (*JournalEntry, error) {
	inputs := make([]PostingInput, len(e.postings))
	for i, p := range e.postings {
		inputs[i] = PostingInput{
			AccountID: p.AccountID(),
			Direction: p.Direction().Opposite(), // invertir
			Amount:    p.Money().Amount(),
			Currency:  p.Money().Currency(),
		}
	}

	reverse, err := NewJournalEntry(
		reverseID,
		e.tenantID,
		reverseIdempotencyKey,
		fmt.Sprintf("Reversa de: %s", e.description),
		map[string]string{"original_entry_id": e.id.String()},
		time.Now().UTC(),
		inputs,
	)
	if err != nil {
		return nil, err
	}

	// Reemplazar el EntryPostedEvent por un EntryReversedEvent en el reverse.
	reverse.events = nil
	reverse.record(EntryReversedEvent{
		baseEvent:       newBase(e.tenantID.String()),
		ReverseEntryID:  reverseID.String(),
		OriginalEntryID: e.id.String(),
		TenantID:        e.tenantID.String(),
	})

	return reverse, nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (e *JournalEntry) ID() EntryID              { return e.id }
func (e *JournalEntry) TenantID() TenantID       { return e.tenantID }
func (e *JournalEntry) IdempotencyKey() string   { return e.idempotencyKey }
func (e *JournalEntry) Description() string      { return e.description }
func (e *JournalEntry) Metadata() map[string]string { return e.metadata }
func (e *JournalEntry) Postings() []Posting      { return e.postings }
func (e *JournalEntry) OccurredAt() time.Time    { return e.occurredAt }
func (e *JournalEntry) CreatedAt() time.Time     { return e.createdAt }

func (e *JournalEntry) PullEvents() []Event {
	evs := e.events
	e.events = nil
	return evs
}

func (e *JournalEntry) record(ev Event) { e.events = append(e.events, ev) }

// ── Validación de doble partida ───────────────────────────────────────────────

// validateBalance verifica que sum(débitos) == sum(créditos).
func validateBalance(postings []Posting) error {
	var debits, credits int64
	for _, p := range postings {
		if p.IsDebit() {
			debits += p.Money().Amount()
		} else {
			credits += p.Money().Amount()
		}
	}
	if debits != credits {
		return fmt.Errorf("%w: debits=%d credits=%d", ErrEntryNotBalanced, debits, credits)
	}
	return nil
}
