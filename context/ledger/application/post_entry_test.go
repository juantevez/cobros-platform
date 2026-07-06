package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func newPostEntryUC(entryRepo *fakeEntryRepo, balRepo *fakeBalanceRepo, pub *fakePublisher) *PostEntryUseCase {
	return NewPostEntryUseCase(entryRepo, balRepo, fakeTx{}, pub, fakeClock{now: time.Unix(1_700_000_000, 0).UTC()})
}

func TestPostEntry_Success(t *testing.T) {
	entryRepo := newFakeEntryRepo()
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newPostEntryUC(entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       validUUID(),
		IdempotencyKey: "pay-123",
		Description:    "pago",
		Lines:          balancedLines(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.EntryID == "" || res.WasExisting {
		t.Errorf("unexpected result: %+v", res)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected entry saved once, got %d", len(entryRepo.saved))
	}
	// Los saldos se aplicaron con los postings del asiento.
	if len(balRepo.applied) != 1 || len(balRepo.applied[0]) != 2 {
		t.Fatalf("balances not applied with 2 postings: %+v", balRepo.applied)
	}
	// Se publicó EntryPostedEvent.
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.EntryPostedEvent); !ok {
		t.Fatalf("expected EntryPostedEvent, got %T", pub.published[0])
	}
}

func TestPostEntry_Idempotent(t *testing.T) {
	tenantID := testTenantID(t)
	existing := buildEntry(t, tenantID, "pay-123")
	entryRepo := newFakeEntryRepo()
	entryRepo.preload(existing)
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newPostEntryUC(entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       tenantID.String(),
		IdempotencyKey: "pay-123",
		Lines:          balancedLines(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.WasExisting || res.EntryID != existing.ID().String() {
		t.Errorf("expected existing entry returned, got %+v", res)
	}
	// No debe guardar, aplicar ni publicar nada nuevo.
	if len(entryRepo.saved) != 0 || len(balRepo.applied) != 0 || len(pub.published) != 0 {
		t.Error("idempotent hit must not save/apply/publish")
	}
}

func TestPostEntry_ValidationErrors(t *testing.T) {
	uc := newPostEntryUC(newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})

	t.Run("missing idempotency key", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: validUUID(), Lines: balancedLines()})
		if err == nil {
			t.Fatal("expected error for missing idempotency key")
		}
	})
	t.Run("fewer than 2 lines", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{
			TenantID: validUUID(), IdempotencyKey: "k",
			Lines: []PostingLine{{AccountID: validUUID(), Direction: "debit", Amount: 100, Currency: "ARS"}},
		})
		if !errors.Is(err, domain.ErrNotEnoughPostings) {
			t.Fatalf("expected ErrNotEnoughPostings, got %v", err)
		}
	})
	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: "nope", IdempotencyKey: "k", Lines: balancedLines()})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid account id in line", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{
			TenantID: validUUID(), IdempotencyKey: "k",
			Lines: []PostingLine{
				{AccountID: "not-uuid", Direction: "debit", Amount: 100, Currency: "ARS"},
				{AccountID: validUUID(), Direction: "credit", Amount: 100, Currency: "ARS"},
			},
		})
		if err == nil {
			t.Fatal("expected error for invalid account id")
		}
	})
	t.Run("invalid direction in line", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{
			TenantID: validUUID(), IdempotencyKey: "k",
			Lines: []PostingLine{
				{AccountID: validUUID(), Direction: "sideways", Amount: 100, Currency: "ARS"},
				{AccountID: validUUID(), Direction: "credit", Amount: 100, Currency: "ARS"},
			},
		})
		if !errors.Is(err, domain.ErrInvalidDirection) {
			t.Fatalf("expected ErrInvalidDirection, got %v", err)
		}
	})
	t.Run("unbalanced entry", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{
			TenantID: validUUID(), IdempotencyKey: "k",
			Lines: []PostingLine{
				{AccountID: validUUID(), Direction: "debit", Amount: 100, Currency: "ARS"},
				{AccountID: validUUID(), Direction: "credit", Amount: 50, Currency: "ARS"},
			},
		})
		if !errors.Is(err, domain.ErrEntryNotBalanced) {
			t.Fatalf("expected ErrEntryNotBalanced, got %v", err)
		}
	})
}

func TestPostEntry_IdempotencyCheckError(t *testing.T) {
	entryRepo := newFakeEntryRepo()
	entryRepo.idempotencyErr = errBoom // error distinto de ErrEntryNotFound
	uc := newPostEntryUC(entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: validUUID(), IdempotencyKey: "k", Lines: balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_SaveErrorPropagates(t *testing.T) {
	entryRepo := newFakeEntryRepo()
	entryRepo.saveErr = errBoom
	uc := newPostEntryUC(entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: validUUID(), IdempotencyKey: "k", Lines: balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_ApplyBalanceErrorPropagates(t *testing.T) {
	balRepo := &fakeBalanceRepo{applyErr: errBoom}
	uc := newPostEntryUC(newFakeEntryRepo(), balRepo, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: validUUID(), IdempotencyKey: "k", Lines: balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_ZeroOccurredAtUsesClock(t *testing.T) {
	entryRepo := newFakeEntryRepo()
	uc := newPostEntryUC(entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: validUUID(), IdempotencyKey: "k", Lines: balancedLines(),
		// OccurredAt cero → usa el clock inyectado
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	saved := entryRepo.saved[0]
	if !saved.OccurredAt().Equal(time.Unix(1_700_000_000, 0).UTC()) {
		t.Errorf("occurredAt = %v, want clock time", saved.OccurredAt())
	}
}

// ── GetBalance ────────────────────────────────────────────────────────────────

func TestGetBalance_Success(t *testing.T) {
	acc, _ := domain.NewAccount(domain.NewAccountID(), testTenantID(t), domain.AccountTypeMerchantBalance, "ARS", "")
	acc.PullEvents()
	accRepo := newFakeAccountRepo(acc)
	balRepo := &fakeBalanceRepo{balance: 9700}
	uc := NewGetBalanceUseCase(accRepo, balRepo)

	res, err := uc.Execute(context.Background(), GetBalanceQuery{AccountID: acc.ID().String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Balance != 9700 || res.Currency != "ARS" || res.AccountID != acc.ID().String() {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestGetBalance_Errors(t *testing.T) {
	t.Run("invalid account id", func(t *testing.T) {
		uc := NewGetBalanceUseCase(newFakeAccountRepo(), &fakeBalanceRepo{})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{AccountID: "nope"}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("account not found", func(t *testing.T) {
		uc := NewGetBalanceUseCase(newFakeAccountRepo(), &fakeBalanceRepo{})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{AccountID: validUUID()}); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Fatalf("expected ErrAccountNotFound, got %v", err)
		}
	})
	t.Run("balance repo error", func(t *testing.T) {
		acc, _ := domain.NewAccount(domain.NewAccountID(), testTenantID(t), domain.AccountTypeReserve, "ARS", "")
		acc.PullEvents()
		uc := NewGetBalanceUseCase(newFakeAccountRepo(acc), &fakeBalanceRepo{balanceErr: errBoom})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{AccountID: acc.ID().String()}); !errors.Is(err, errBoom) {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestRealClock(t *testing.T) {
	before := time.Now().UTC()
	got := RealClock().Now()
	after := time.Now().UTC()
	if got.Before(before) || got.After(after) {
		t.Errorf("RealClock().Now() = %v out of range", got)
	}
}
