package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func newPostEntryUC(accRepo *fakeAccountRepo, entryRepo *fakeEntryRepo, balRepo *fakeBalanceRepo, pub *fakePublisher) *PostEntryUseCase {
	return NewPostEntryUseCase(entryRepo, accRepo, balRepo, fakeTx{}, pub, fakeClock{now: time.Unix(1_700_000_000, 0).UTC()})
}

func TestPostEntry_Success(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newPostEntryUC(f.accounts, entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       f.tenantID.String(),
		IdempotencyKey: "pay-123",
		Description:    "pago",
		Lines:          f.balancedLines(),
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
	f := newEntryFixture(t)
	existing := f.entry(t, "pay-123", 100)
	entryRepo := newFakeEntryRepo()
	entryRepo.preload(existing)
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newPostEntryUC(f.accounts, entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       f.tenantID.String(),
		IdempotencyKey: "pay-123",
		Lines:          f.balancedLines(),
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

// Reusar la clave con otros movimientos no devuelve el asiento original.
func TestPostEntry_IdempotencyKeyConflict(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	entryRepo.preload(f.entry(t, "pay-123", 100))
	balRepo := &fakeBalanceRepo{}
	uc := newPostEntryUC(f.accounts, entryRepo, balRepo, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       f.tenantID.String(),
		IdempotencyKey: "pay-123",
		Lines:          f.lines(250, 250),
	})
	if !errors.Is(err, domain.ErrIdempotencyKeyConflict) {
		t.Fatalf("expected ErrIdempotencyKeyConflict, got %v", err)
	}
	if len(entryRepo.saved) != 0 || len(balRepo.applied) != 0 {
		t.Error("conflict must not save or apply")
	}
}

// Dos requests concurrentes con la misma clave: el que pierde el UNIQUE
// devuelve el asiento del otro en lugar de un error.
func TestPostEntry_RaceReturnsExisting(t *testing.T) {
	f := newEntryFixture(t)
	winner := f.entry(t, "pay-123", 100)
	entryRepo := newFakeEntryRepo()
	entryRepo.raceEntry = winner
	uc := newPostEntryUC(f.accounts, entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	res, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       f.tenantID.String(),
		IdempotencyKey: "pay-123",
		Lines:          f.balancedLines(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.WasExisting || res.EntryID != winner.ID().String() {
		t.Errorf("expected winner entry, got %+v", res)
	}
}

func TestPostEntry_RaceWithDifferentContentConflicts(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	entryRepo.raceEntry = f.entry(t, "pay-123", 999)
	uc := newPostEntryUC(f.accounts, entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID:       f.tenantID.String(),
		IdempotencyKey: "pay-123",
		Lines:          f.balancedLines(),
	})
	if !errors.Is(err, domain.ErrIdempotencyKeyConflict) {
		t.Fatalf("expected ErrIdempotencyKeyConflict, got %v", err)
	}
}

func TestPostEntry_ValidationErrors(t *testing.T) {
	f := newEntryFixture(t)
	uc := newPostEntryUC(f.accounts, newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})
	tenant := f.tenantID.String()

	t.Run("missing idempotency key", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: tenant, Lines: f.balancedLines()})
		if !errors.Is(err, domain.ErrIdempotencyKeyRequired) {
			t.Fatalf("expected ErrIdempotencyKeyRequired, got %v", err)
		}
	})
	t.Run("fewer than 2 lines", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{
			TenantID: tenant, IdempotencyKey: "k",
			Lines: f.balancedLines()[:1],
		})
		if !errors.Is(err, domain.ErrNotEnoughPostings) {
			t.Fatalf("expected ErrNotEnoughPostings, got %v", err)
		}
	})
	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: "nope", IdempotencyKey: "k", Lines: f.balancedLines()})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid account id in line", func(t *testing.T) {
		lines := f.balancedLines()
		lines[0].AccountID = "not-uuid"
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: tenant, IdempotencyKey: "k", Lines: lines})
		if err == nil {
			t.Fatal("expected error for invalid account id")
		}
	})
	t.Run("invalid direction in line", func(t *testing.T) {
		lines := f.balancedLines()
		lines[0].Direction = "sideways"
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: tenant, IdempotencyKey: "k", Lines: lines})
		if !errors.Is(err, domain.ErrInvalidDirection) {
			t.Fatalf("expected ErrInvalidDirection, got %v", err)
		}
	})
	t.Run("unbalanced entry", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: tenant, IdempotencyKey: "k", Lines: f.lines(100, 50)})
		if !errors.Is(err, domain.ErrEntryNotBalanced) {
			t.Fatalf("expected ErrEntryNotBalanced, got %v", err)
		}
	})
}

// ── Validación de cuentas ─────────────────────────────────────────────────────

func TestPostEntry_RejectsUnknownAccount(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	balRepo := &fakeBalanceRepo{}
	uc := newPostEntryUC(f.accounts, entryRepo, balRepo, &fakePublisher{})

	lines := f.balancedLines()
	lines[1].AccountID = validUUID() // no existe
	_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: lines})
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
	if len(entryRepo.saved) != 0 || len(balRepo.applied) != 0 {
		t.Error("nothing should be saved or applied")
	}
}

// Postear contra una cuenta de otro tenant movería su saldo (el UPDATE es por
// account_id). Debe rechazarse como inexistente, sin revelar que existe.
func TestPostEntry_RejectsAccountOfAnotherTenant(t *testing.T) {
	f := newEntryFixture(t)
	foreign := newTestAccount(t, testTenantID(t), domain.AccountTypeMerchantBalance, "ARS")
	f.accounts.byID[foreign.ID()] = foreign
	entryRepo := newFakeEntryRepo()
	balRepo := &fakeBalanceRepo{}
	uc := newPostEntryUC(f.accounts, entryRepo, balRepo, &fakePublisher{})

	lines := f.balancedLines()
	lines[1].AccountID = foreign.ID().String()
	_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: lines})
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
	if len(entryRepo.saved) != 0 || len(balRepo.applied) != 0 {
		t.Error("nothing should be saved or applied")
	}
}

func TestPostEntry_RejectsCurrencyDifferentFromAccount(t *testing.T) {
	f := newEntryFixture(t) // cuentas ARS
	uc := newPostEntryUC(f.accounts, newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})

	lines := f.balancedLines()
	for i := range lines {
		lines[i].Currency = "USD" // asiento consistente en USD, pero las cuentas son ARS
	}
	_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: lines})
	if !errors.Is(err, domain.ErrAccountCurrencyMismatch) {
		t.Fatalf("expected ErrAccountCurrencyMismatch, got %v", err)
	}
}

func TestPostEntry_AccountLookupErrorPropagates(t *testing.T) {
	f := newEntryFixture(t)
	f.accounts.findErr = errBoom
	uc := newPostEntryUC(f.accounts, newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: f.balancedLines()})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

// ── Errores de infraestructura ────────────────────────────────────────────────

func TestPostEntry_IdempotencyCheckError(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	entryRepo.idempotencyErr = errBoom // error distinto de ErrEntryNotFound
	uc := newPostEntryUC(f.accounts, entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: f.balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_SaveErrorPropagates(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	entryRepo.saveErr = errBoom
	uc := newPostEntryUC(f.accounts, entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: f.balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_ApplyBalanceErrorPropagates(t *testing.T) {
	f := newEntryFixture(t)
	balRepo := &fakeBalanceRepo{applyErr: errBoom}
	uc := newPostEntryUC(f.accounts, newFakeEntryRepo(), balRepo, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: f.balancedLines(),
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestPostEntry_ZeroOccurredAtUsesClock(t *testing.T) {
	f := newEntryFixture(t)
	entryRepo := newFakeEntryRepo()
	uc := newPostEntryUC(f.accounts, entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), PostEntryCmd{
		TenantID: f.tenantID.String(), IdempotencyKey: "k", Lines: f.balancedLines(),
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
	tenantID := testTenantID(t)
	acc := newTestAccount(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accRepo := newFakeAccountRepo(acc)
	balRepo := &fakeBalanceRepo{balance: 9700}
	uc := NewGetBalanceUseCase(accRepo, balRepo)

	res, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: tenantID.String(), AccountID: acc.ID().String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Balance != 9700 || res.Currency != "ARS" || res.AccountID != acc.ID().String() {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestGetBalance_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("invalid tenant id", func(t *testing.T) {
		uc := NewGetBalanceUseCase(newFakeAccountRepo(), &fakeBalanceRepo{})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: "nope", AccountID: validUUID()}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid account id", func(t *testing.T) {
		uc := NewGetBalanceUseCase(newFakeAccountRepo(), &fakeBalanceRepo{})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: tenantID.String(), AccountID: "nope"}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("account not found", func(t *testing.T) {
		uc := NewGetBalanceUseCase(newFakeAccountRepo(), &fakeBalanceRepo{})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: tenantID.String(), AccountID: validUUID()}); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Fatalf("expected ErrAccountNotFound, got %v", err)
		}
	})
	t.Run("account of another tenant is not found", func(t *testing.T) {
		foreign := newTestAccount(t, testTenantID(t), domain.AccountTypeMerchantBalance, "ARS")
		uc := NewGetBalanceUseCase(newFakeAccountRepo(foreign), &fakeBalanceRepo{balance: 1})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: tenantID.String(), AccountID: foreign.ID().String()}); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Fatalf("expected ErrAccountNotFound, got %v", err)
		}
	})
	t.Run("balance repo error", func(t *testing.T) {
		acc := newTestAccount(t, tenantID, domain.AccountTypeReserve, "ARS")
		uc := NewGetBalanceUseCase(newFakeAccountRepo(acc), &fakeBalanceRepo{balanceErr: errBoom})
		if _, err := uc.Execute(context.Background(), GetBalanceQuery{TenantID: tenantID.String(), AccountID: acc.ID().String()}); !errors.Is(err, errBoom) {
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
