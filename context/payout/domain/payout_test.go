package domain

import (
	"errors"
	"testing"
	"time"
)

const testTenant = TenantID("11111111-1111-1111-1111-111111111111")

func testBankAccount() BankAccountInfo {
	return BankAccountInfo{
		AccountType:   "CBU",
		accountNumber: "0170099220000067797370",
		BankName:      "Banco Test",
		HolderName:    "Comercio SA",
		Currency:      "ARS",
	}
}

// newTestPayout crea un payout en initiated y descarta el evento inicial.
func newTestPayout(t *testing.T) *Payout {
	t.Helper()
	money, err := NewMoney(5000, "ARS")
	if err != nil {
		t.Fatalf("NewMoney: %v", err)
	}
	p, err := NewPayout(NewPayoutID(), testTenant, money, testBankAccount(), "test-key")
	if err != nil {
		t.Fatalf("NewPayout: %v", err)
	}
	p.PullEvents()
	return p
}

func newProcessingPayout(t *testing.T) *Payout {
	t.Helper()
	p := newTestPayout(t)
	if err := p.MarkProcessing(); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
	return p
}

func TestNewPayout(t *testing.T) {
	id := NewPayoutID()
	money, _ := NewMoney(5000, "ARS")
	before := time.Now().UTC()

	p, err := NewPayout(id, testTenant, money, testBankAccount(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.ID() != id || p.TenantID() != testTenant {
		t.Errorf("ids not set: %v %v", p.ID(), p.TenantID())
	}
	if p.Amount() != money {
		t.Errorf("amount = %v, want %v", p.Amount(), money)
	}
	if p.BankAccount() != testBankAccount() {
		t.Errorf("bank account = %+v", p.BankAccount())
	}
	if p.Status() != StatusInitiated {
		t.Errorf("status = %q, want initiated", p.Status())
	}
	if want := "payout_initiated_" + id.String(); p.LedgerEntryKey() != want {
		t.Errorf("ledger key = %q, want %q", p.LedgerEntryKey(), want)
	}
	if p.BankReference() != "" || p.FailureReason() != "" {
		t.Error("reference and failure reason should be empty")
	}
	if p.InitiatedAt() != nil || p.ConfirmedAt() != nil || p.FailedAt() != nil {
		t.Error("lifecycle timestamps should be nil on creation")
	}
	if p.CreatedAt().Before(before) || !p.CreatedAt().Equal(p.UpdatedAt()) {
		t.Errorf("createdAt=%v updatedAt=%v", p.CreatedAt(), p.UpdatedAt())
	}
	if p.CreatedAt().Location() != time.UTC {
		t.Error("createdAt should be UTC")
	}

	evs := p.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev, ok := evs[0].(PayoutInitiatedEvent)
	if !ok {
		t.Fatalf("expected PayoutInitiatedEvent, got %T", evs[0])
	}
	if ev.PayoutID != id.String() || ev.TenantID != testTenant.String() ||
		ev.Amount != 5000 || ev.Currency != "ARS" || ev.IdempotencyKey != p.LedgerEntryKey() {
		t.Errorf("event fields not set: %+v", ev)
	}
	if ev.EventTenantID() != testTenant.String() {
		t.Errorf("event tenant = %q", ev.EventTenantID())
	}
}

func TestPayout_PullEvents(t *testing.T) {
	money, _ := NewMoney(100, "ARS")
	p, _ := NewPayout(NewPayoutID(), testTenant, money, testBankAccount(), "test-key")

	if got := len(p.PullEvents()); got != 1 {
		t.Fatalf("first pull = %d events, want 1", got)
	}
	if got := p.PullEvents(); len(got) != 0 {
		t.Errorf("second pull should be empty, got %d", len(got))
	}
}

func TestPayout_MarkProcessing(t *testing.T) {
	t.Run("from initiated", func(t *testing.T) {
		p := newTestPayout(t)
		created := p.UpdatedAt()

		if err := p.MarkProcessing(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusProcessing {
			t.Errorf("status = %q, want processing", p.Status())
		}
		if p.InitiatedAt() == nil {
			t.Fatal("initiatedAt should be set")
		}
		if !p.UpdatedAt().Equal(*p.InitiatedAt()) || p.UpdatedAt().Before(created) {
			t.Errorf("updatedAt=%v initiatedAt=%v", p.UpdatedAt(), *p.InitiatedAt())
		}
		if len(p.PullEvents()) != 0 {
			t.Error("MarkProcessing should not emit events")
		}
	})

	for _, st := range []PayoutStatus{StatusProcessing, StatusConfirmed, StatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.MarkProcessing(); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st {
				t.Errorf("status changed to %q", p.Status())
			}
		})
	}
}

func TestPayout_Confirm(t *testing.T) {
	t.Run("from processing", func(t *testing.T) {
		p := newProcessingPayout(t)

		if err := p.Confirm("BANK-123"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusConfirmed {
			t.Errorf("status = %q, want confirmed", p.Status())
		}
		if p.BankReference() != "BANK-123" {
			t.Errorf("bank reference = %q", p.BankReference())
		}
		if p.ConfirmedAt() == nil || !p.UpdatedAt().Equal(*p.ConfirmedAt()) {
			t.Errorf("confirmedAt=%v updatedAt=%v", p.ConfirmedAt(), p.UpdatedAt())
		}
		if p.FailedAt() != nil {
			t.Error("failedAt should stay nil")
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PayoutConfirmedEvent)
		if !ok {
			t.Fatalf("expected PayoutConfirmedEvent, got %T", evs[0])
		}
		if ev.PayoutID != p.ID().String() || ev.TenantID != testTenant.String() ||
			ev.Amount != 5000 || ev.Currency != "ARS" || ev.BankReference != "BANK-123" {
			t.Errorf("event fields not set: %+v", ev)
		}
	})

	for _, st := range []PayoutStatus{StatusInitiated, StatusConfirmed, StatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.Confirm("REF"); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st || len(p.PullEvents()) != 0 {
				t.Error("rejected transition must not change state or emit events")
			}
		})
	}
}

func TestPayout_Fail(t *testing.T) {
	t.Run("from processing", func(t *testing.T) {
		p := newProcessingPayout(t)

		if err := p.Fail("account closed"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Status() != StatusFailed {
			t.Errorf("status = %q, want failed", p.Status())
		}
		if p.FailureReason() != "account closed" {
			t.Errorf("failure reason = %q", p.FailureReason())
		}
		if p.FailedAt() == nil || !p.UpdatedAt().Equal(*p.FailedAt()) {
			t.Errorf("failedAt=%v updatedAt=%v", p.FailedAt(), p.UpdatedAt())
		}
		if p.ConfirmedAt() != nil {
			t.Error("confirmedAt should stay nil")
		}

		evs := p.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(PayoutFailedEvent)
		if !ok {
			t.Fatalf("expected PayoutFailedEvent, got %T", evs[0])
		}
		if ev.PayoutID != p.ID().String() || ev.TenantID != testTenant.String() ||
			ev.Amount != 5000 || ev.Currency != "ARS" || ev.FailureReason != "account closed" {
			t.Errorf("event fields not set: %+v", ev)
		}
	})

	t.Run("empty reason rejected without changing state", func(t *testing.T) {
		p := newProcessingPayout(t)

		if err := p.Fail(""); !errors.Is(err, ErrFailureReasonRequired) {
			t.Fatalf("expected ErrFailureReasonRequired, got %v", err)
		}
		if p.Status() != StatusProcessing || p.FailedAt() != nil || len(p.PullEvents()) != 0 {
			t.Error("payout must stay processing with no events")
		}
	})

	t.Run("transition checked before reason", func(t *testing.T) {
		p := newTestPayout(t)
		if err := p.Fail(""); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	for _, st := range []PayoutStatus{StatusInitiated, StatusConfirmed, StatusFailed} {
		t.Run("rejected from "+st.String(), func(t *testing.T) {
			p := reconstituteWithStatus(st)
			if err := p.Fail("reason"); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
			if p.Status() != st || len(p.PullEvents()) != 0 {
				t.Error("rejected transition must not change state or emit events")
			}
		})
	}
}

func TestReconstitutePayout(t *testing.T) {
	id := NewPayoutID()
	money := ReconstituteMoney(9900, "USD")
	initiated := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	failed := initiated.Add(time.Minute)
	created := initiated.Add(-time.Minute)

	p := ReconstitutePayout(
		id, testTenant, money, testBankAccount(),
		StatusFailed,
		"REF", "rejected", "ledger-key",
		&initiated, nil, &failed,
		created, failed,
	)

	if p.ID() != id || p.TenantID() != testTenant || p.Amount() != money || p.BankAccount() != testBankAccount() {
		t.Errorf("identity/amount not restored: %+v", p)
	}
	if p.Status() != StatusFailed || p.BankReference() != "REF" ||
		p.FailureReason() != "rejected" || p.LedgerEntryKey() != "ledger-key" {
		t.Errorf("state not restored: %+v", p)
	}
	if p.InitiatedAt() != &initiated || p.ConfirmedAt() != nil || p.FailedAt() != &failed {
		t.Error("lifecycle timestamps not restored")
	}
	if !p.CreatedAt().Equal(created) || !p.UpdatedAt().Equal(failed) {
		t.Errorf("createdAt=%v updatedAt=%v", p.CreatedAt(), p.UpdatedAt())
	}
	if len(p.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func reconstituteWithStatus(st PayoutStatus) *Payout {
	now := time.Now().UTC()
	return ReconstitutePayout(
		NewPayoutID(), testTenant, ReconstituteMoney(5000, "ARS"), testBankAccount(),
		st, "", "", "key",
		nil, nil, nil,
		now, now,
	)
}
