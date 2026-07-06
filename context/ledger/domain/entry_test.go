package domain

import (
	"errors"
	"testing"
	"time"
)

func balancedInputs() []PostingInput {
	return []PostingInput{
		{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 100, Currency: "ARS"},
		{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 100, Currency: "ARS"},
	}
}

func newBalancedEntry(t *testing.T) *JournalEntry {
	t.Helper()
	e, err := NewJournalEntry(
		NewEntryID(), NewTenantID_forTest(t), "idem-1", "pago", nil,
		time.Now().UTC(), balancedInputs(),
	)
	if err != nil {
		t.Fatalf("build entry: %v", err)
	}
	return e
}

func TestNewJournalEntry_Success(t *testing.T) {
	tid := NewTenantID_forTest(t)
	e, err := NewJournalEntry(NewEntryID(), tid, "idem-42", "pago acreditado", map[string]string{"order": "1234"},
		time.Now().UTC(), balancedInputs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(e.Postings()) != 2 {
		t.Fatalf("expected 2 postings, got %d", len(e.Postings()))
	}
	if e.IdempotencyKey() != "idem-42" {
		t.Errorf("idempotency key = %q", e.IdempotencyKey())
	}
	if e.TenantID() != tid || e.Description() != "pago acreditado" {
		t.Errorf("tenant/description mismatch: %q %q", e.TenantID(), e.Description())
	}
	if e.Metadata()["order"] != "1234" {
		t.Errorf("metadata mismatch: %v", e.Metadata())
	}

	events := e.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	posted, ok := events[0].(EntryPostedEvent)
	if !ok {
		t.Fatalf("expected EntryPostedEvent, got %T", events[0])
	}
	if posted.EntryID != e.ID().String() || len(posted.Postings) != 2 {
		t.Errorf("event payload mismatch: %+v", posted)
	}
}

func TestNewJournalEntry_ThreePostingsBalanced(t *testing.T) {
	// El ejemplo real: credit 10000 = debit 9700 + debit 300.
	inputs := []PostingInput{
		{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 10000, Currency: "ARS"},
		{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 9700, Currency: "ARS"},
		{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 300, Currency: "ARS"},
	}
	if _, err := NewJournalEntry(NewEntryID(), NewTenantID_forTest(t), "k", "d", nil, time.Now().UTC(), inputs); err != nil {
		t.Fatalf("balanced 3-posting entry rejected: %v", err)
	}
}

func TestNewJournalEntry_Invariants(t *testing.T) {
	tid := NewTenantID_forTest(t)
	mk := func(inputs []PostingInput) error {
		_, err := NewJournalEntry(NewEntryID(), tid, "k", "d", nil, time.Now().UTC(), inputs)
		return err
	}

	t.Run("fewer than 2 postings", func(t *testing.T) {
		err := mk([]PostingInput{{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 100, Currency: "ARS"}})
		if !errors.Is(err, ErrNotEnoughPostings) {
			t.Fatalf("expected ErrNotEnoughPostings, got %v", err)
		}
	})

	t.Run("unbalanced debits != credits", func(t *testing.T) {
		err := mk([]PostingInput{
			{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 100, Currency: "ARS"},
			{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 50, Currency: "ARS"},
		})
		if !errors.Is(err, ErrEntryNotBalanced) {
			t.Fatalf("expected ErrEntryNotBalanced, got %v", err)
		}
	})

	t.Run("currency mismatch across postings", func(t *testing.T) {
		err := mk([]PostingInput{
			{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 100, Currency: "ARS"},
			{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 100, Currency: "USD"},
		})
		if !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
		}
	})

	t.Run("zero amount posting", func(t *testing.T) {
		err := mk([]PostingInput{
			{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 0, Currency: "ARS"},
			{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 0, Currency: "ARS"},
		})
		if !errors.Is(err, ErrZeroAmount) {
			t.Fatalf("expected ErrZeroAmount, got %v", err)
		}
	})

	t.Run("invalid currency in a posting", func(t *testing.T) {
		err := mk([]PostingInput{
			{AccountID: NewAccountID(), Direction: DirectionDebit, Amount: 100, Currency: "XX"},
			{AccountID: NewAccountID(), Direction: DirectionCredit, Amount: 100, Currency: "XX"},
		})
		if !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
	})
}

func TestPosting_Getters(t *testing.T) {
	e := newBalancedEntry(t)
	var sawDebit, sawCredit bool
	for _, p := range e.Postings() {
		if p.IsDebit() {
			sawDebit = true
			if p.Direction() != DirectionDebit {
				t.Error("IsDebit true but direction not debit")
			}
		}
		if p.IsCredit() {
			sawCredit = true
		}
		if p.Money().Amount() != 100 {
			t.Errorf("amount = %d, want 100", p.Money().Amount())
		}
		if p.ID().String() == "" {
			t.Error("posting id should be set")
		}
	}
	if !sawDebit || !sawCredit {
		t.Error("expected both a debit and a credit posting")
	}
}

func TestBuildReverse(t *testing.T) {
	// Original: debit A 100, credit B 100.
	accA, accB := NewAccountID(), NewAccountID()
	original, err := NewJournalEntry(NewEntryID(), NewTenantID_forTest(t), "orig", "pago", nil, time.Now().UTC(),
		[]PostingInput{
			{AccountID: accA, Direction: DirectionDebit, Amount: 100, Currency: "ARS"},
			{AccountID: accB, Direction: DirectionCredit, Amount: 100, Currency: "ARS"},
		})
	if err != nil {
		t.Fatalf("build original: %v", err)
	}
	original.PullEvents()

	reverseID := NewEntryID()
	reverse, err := original.BuildReverse(reverseID, "rev-1")
	if err != nil {
		t.Fatalf("build reverse: %v", err)
	}

	// Direcciones invertidas, mismos montos y cuentas.
	rp := reverse.Postings()
	if rp[0].AccountID() != accA || rp[0].Direction() != DirectionCredit {
		t.Errorf("posting 0 should be credit A, got %s %s", rp[0].AccountID(), rp[0].Direction())
	}
	if rp[1].AccountID() != accB || rp[1].Direction() != DirectionDebit {
		t.Errorf("posting 1 should be debit B, got %s %s", rp[1].AccountID(), rp[1].Direction())
	}

	// Metadata enlaza al asiento original.
	if reverse.Metadata()["original_entry_id"] != original.ID().String() {
		t.Errorf("metadata original_entry_id = %q", reverse.Metadata()["original_entry_id"])
	}

	// Emite EntryReversedEvent (no EntryPostedEvent).
	events := reverse.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	reversed, ok := events[0].(EntryReversedEvent)
	if !ok {
		t.Fatalf("expected EntryReversedEvent, got %T", events[0])
	}
	if reversed.OriginalEntryID != original.ID().String() || reversed.ReverseEntryID != reverseID.String() {
		t.Errorf("reversed event payload mismatch: %+v", reversed)
	}
}

func TestReconstituteJournalEntry(t *testing.T) {
	id := NewEntryID()
	tid := NewTenantID_forTest(t)
	postings := []Posting{
		ReconstitutePosting(NewPostingID(), NewAccountID(), DirectionDebit, MustMoney(100, "ARS")),
		ReconstitutePosting(NewPostingID(), NewAccountID(), DirectionCredit, MustMoney(100, "ARS")),
	}
	occurred := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	e := ReconstituteJournalEntry(id, tid, "k", "d", nil, postings, occurred, created)

	if e.ID() != id || len(e.Postings()) != 2 {
		t.Error("fields not restored")
	}
	if !e.OccurredAt().Equal(occurred) || !e.CreatedAt().Equal(created) {
		t.Error("timestamps not restored")
	}
	if len(e.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}
