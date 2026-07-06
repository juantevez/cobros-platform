package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

func newReverseUC(entryRepo *fakeEntryRepo, balRepo *fakeBalanceRepo, pub *fakePublisher) *ReverseEntryUseCase {
	return NewReverseEntryUseCase(entryRepo, balRepo, fakeTx{}, pub)
}

func TestReverseEntry_Success(t *testing.T) {
	tenantID := testTenantID(t)
	original := buildEntry(t, tenantID, "orig-key")
	entryRepo := newFakeEntryRepo()
	entryRepo.byID[original.ID()] = original // solo indexado por ID (sin reverse previo)
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newReverseUC(entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), ReverseEntryCmd{
		TenantID:        tenantID.String(),
		OriginalEntryID: original.ID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ReverseEntryID == "" {
		t.Fatal("expected a reverse entry id")
	}
	// Se guardó el reverso, se aplicaron saldos y se publicó EntryReversedEvent.
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected reverse saved, got %d", len(entryRepo.saved))
	}
	if entryRepo.saved[0].IdempotencyKey() != "reverse_orig-key" {
		t.Errorf("reverse key = %q, want reverse_orig-key", entryRepo.saved[0].IdempotencyKey())
	}
	if len(balRepo.applied) != 1 {
		t.Fatal("reverse balances not applied")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.EntryReversedEvent); !ok {
		t.Fatalf("expected EntryReversedEvent, got %T", pub.published[0])
	}
}

func TestReverseEntry_Idempotent(t *testing.T) {
	tenantID := testTenantID(t)
	original := buildEntry(t, tenantID, "orig-key")
	existingReverse := buildEntry(t, tenantID, "reverse_orig-key")
	entryRepo := newFakeEntryRepo()
	entryRepo.byID[original.ID()] = original
	entryRepo.preload(existingReverse) // ya existe el reverso
	balRepo := &fakeBalanceRepo{}
	pub := &fakePublisher{}
	uc := newReverseUC(entryRepo, balRepo, pub)

	res, err := uc.Execute(context.Background(), ReverseEntryCmd{
		TenantID:        tenantID.String(),
		OriginalEntryID: original.ID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ReverseEntryID != existingReverse.ID().String() {
		t.Errorf("expected existing reverse, got %s", res.ReverseEntryID)
	}
	if len(entryRepo.saved) != 0 || len(balRepo.applied) != 0 || len(pub.published) != 0 {
		t.Error("idempotent reverse must not save/apply/publish")
	}
}

func TestReverseEntry_Errors(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("invalid tenant id", func(t *testing.T) {
		uc := newReverseUC(newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})
		_, err := uc.Execute(context.Background(), ReverseEntryCmd{TenantID: "nope", OriginalEntryID: validUUID()})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid original entry id", func(t *testing.T) {
		uc := newReverseUC(newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})
		_, err := uc.Execute(context.Background(), ReverseEntryCmd{TenantID: tenantID.String(), OriginalEntryID: "nope"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("original not found", func(t *testing.T) {
		uc := newReverseUC(newFakeEntryRepo(), &fakeBalanceRepo{}, &fakePublisher{})
		_, err := uc.Execute(context.Background(), ReverseEntryCmd{TenantID: tenantID.String(), OriginalEntryID: validUUID()})
		if !errors.Is(err, domain.ErrEntryNotFound) {
			t.Fatalf("expected ErrEntryNotFound, got %v", err)
		}
	})

	t.Run("original belongs to another tenant", func(t *testing.T) {
		otherTenant := testTenantID(t)
		original := buildEntry(t, otherTenant, "orig-key")
		entryRepo := newFakeEntryRepo()
		entryRepo.byID[original.ID()] = original
		uc := newReverseUC(entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

		_, err := uc.Execute(context.Background(), ReverseEntryCmd{
			TenantID:        tenantID.String(), // distinto del dueño
			OriginalEntryID: original.ID().String(),
		})
		if !errors.Is(err, domain.ErrEntryNotFound) {
			t.Fatalf("expected ErrEntryNotFound (isolation), got %v", err)
		}
	})

	t.Run("save error propagates", func(t *testing.T) {
		original := buildEntry(t, tenantID, "orig-key")
		entryRepo := newFakeEntryRepo()
		entryRepo.byID[original.ID()] = original
		entryRepo.saveErr = errBoom
		uc := newReverseUC(entryRepo, &fakeBalanceRepo{}, &fakePublisher{})

		_, err := uc.Execute(context.Background(), ReverseEntryCmd{
			TenantID: tenantID.String(), OriginalEntryID: original.ID().String(),
		})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
