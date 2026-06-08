package domain

import "errors"

var (
	// ── Account ──────────────────────────────────────────────────────────────

	ErrAccountNotFound    = errors.New("account not found")
	ErrInvalidAccountType = errors.New("invalid account type")
	ErrCurrencyMismatch   = errors.New("all postings in an entry must share the same currency")

	// ── JournalEntry ─────────────────────────────────────────────────────────

	ErrEntryNotFound      = errors.New("journal entry not found")
	ErrEntryNotBalanced   = errors.New("journal entry is not balanced: sum(debits) must equal sum(credits)")
	ErrNotEnoughPostings  = errors.New("a journal entry requires at least two postings")
	ErrZeroAmount         = errors.New("posting amount must be greater than zero")
	ErrInvalidDirection   = errors.New("invalid posting direction: must be 'debit' or 'credit'")
	ErrEntryAlreadyExists = errors.New("a journal entry with this idempotency key already exists")

	// ── Money ────────────────────────────────────────────────────────────────

	ErrInvalidCurrency  = errors.New("invalid currency code: must be 3-letter ISO 4217")
	ErrNegativeAmount   = errors.New("money amount cannot be negative")
	ErrInvalidMoneyOp   = errors.New("cannot operate on money with different currencies")
)
