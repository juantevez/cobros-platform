package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

func TestRecordActionUseCase_Execute(t *testing.T) {
	validCmd := RecordActionCmd{
		TenantID:      "tenant-1",
		Actor:         "user-42",
		Action:        "auth.user.login",
		ResourceType:  "user",
		ResourceID:    "user-42",
		Metadata:      map[string]string{"ip": "10.0.0.1"},
		CorrelationID: "corr-1",
	}

	t.Run("first entry has nil prevHash and is saved", func(t *testing.T) {
		repo := &fakeRepo{last: nil}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})

		if err := uc.Execute(context.Background(), validCmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.saved) != 1 {
			t.Fatalf("expected 1 saved entry, got %d", len(repo.saved))
		}
		got := repo.saved[0]
		if len(got.PrevHash()) != 0 {
			t.Errorf("first entry should have nil prevHash, got %x", got.PrevHash())
		}
		if got.Actor() != "user-42" || got.TenantID() != "tenant-1" {
			t.Errorf("fields not propagated: %+v", got)
		}
		if !got.CreatedAt().Equal(testNow) {
			t.Errorf("clock not used: %v", got.CreatedAt())
		}
	})

	t.Run("subsequent entry links to last hash", func(t *testing.T) {
		prev := buildEntry("t", domain.ActionTenantCreated, domain.ResourceTenant, "t1", nil)
		repo := &fakeRepo{last: prev}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})

		if err := uc.Execute(context.Background(), validCmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := repo.saved[0]
		if !got.ChainLinksTo(prev) {
			t.Error("new entry does not link to last entry")
		}
	})

	t.Run("invalid action rejected before touching repo", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		cmd := validCmd
		cmd.Action = ""

		err := uc.Execute(context.Background(), cmd)
		if !errors.Is(err, domain.ErrInvalidAction) {
			t.Fatalf("expected ErrInvalidAction, got %v", err)
		}
		if len(repo.saved) != 0 {
			t.Error("nothing should be saved on validation error")
		}
	})

	t.Run("invalid resource type rejected", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		cmd := validCmd
		cmd.ResourceType = "bogus"

		err := uc.Execute(context.Background(), cmd)
		if !errors.Is(err, domain.ErrInvalidResourceType) {
			t.Fatalf("expected ErrInvalidResourceType, got %v", err)
		}
		if len(repo.saved) != 0 {
			t.Error("nothing should be saved on validation error")
		}
	})

	t.Run("FindLast error propagated", func(t *testing.T) {
		repo := &fakeRepo{findLastErr: errBoom}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})

		err := uc.Execute(context.Background(), validCmd)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("Save error propagated", func(t *testing.T) {
		repo := &fakeRepo{saveErr: errBoom}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})

		err := uc.Execute(context.Background(), validCmd)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("empty actor defaults to system", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		cmd := validCmd
		cmd.Actor = ""

		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.saved[0].Actor() != "system" {
			t.Errorf("actor = %q, want system", repo.saved[0].Actor())
		}
	})

	t.Run("saved entry passes its own hash verification", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})

		if err := uc.Execute(context.Background(), validCmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !repo.saved[0].VerifyHash(newHasher().Compute) {
			t.Error("recorded entry fails hash verification")
		}
	})

	t.Run("lock, read last and insert happen in one transaction, in order", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		if err := uc.Execute(context.Background(), RecordActionCmd{
			Action: "auth.tenant.created", ResourceType: "tenant", ResourceID: "t1",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "lock:tx,find:tx,save:tx"
		if got := strings.Join(repo.calls, ","); got != want {
			t.Errorf("calls = %s, want %s", got, want)
		}
	})

	t.Run("lock error aborts before reading", func(t *testing.T) {
		repo := &fakeRepo{lockErr: errors.New("boom")}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		err := uc.Execute(context.Background(), RecordActionCmd{
			Action: "auth.tenant.created", ResourceType: "tenant", ResourceID: "t1",
		})
		if err == nil || len(repo.saved) != 0 || len(repo.calls) != 1 {
			t.Fatalf("err=%v calls=%v", err, repo.calls)
		}
	})

	t.Run("tx error propagated", func(t *testing.T) {
		uc := NewRecordActionUseCase(&fakeRepo{}, newHasher(), newClock(), fakeTx{err: errors.New("db down")})
		if err := uc.Execute(context.Background(), RecordActionCmd{
			Action: "auth.tenant.created", ResourceType: "tenant", ResourceID: "t1",
		}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("already recorded event is a no-op (checked under the lock)", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		cmd := RecordActionCmd{Action: "auth.tenant.created", ResourceType: "tenant", ResourceID: "t1", EventID: "ev-1"}
		for i := 0; i < 2; i++ {
			if err := uc.Execute(context.Background(), cmd); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if len(repo.saved) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(repo.saved))
		}
		want := "lock:tx,exists:tx,find:tx,save:tx,lock:tx,exists:tx"
		if got := strings.Join(repo.calls, ","); got != want {
			t.Errorf("calls = %s, want %s", got, want)
		}
	})

	t.Run("actions without event id skip the idempotency check", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		_ = uc.Execute(context.Background(), RecordActionCmd{Action: "auth.tenant.created", ResourceType: "tenant"})
		if strings.Contains(strings.Join(repo.calls, ","), "exists") {
			t.Errorf("calls = %v", repo.calls)
		}
	})

	t.Run("event time is kept apart from the registration time", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewRecordActionUseCase(repo, newHasher(), newClock(), fakeTx{})
		occurred := testNow.Add(-2 * time.Hour)
		_ = uc.Execute(context.Background(), RecordActionCmd{
			Action: "auth.tenant.created", ResourceType: "tenant", EventID: "ev-9", OccurredAt: occurred,
		})
		e := repo.saved[0]
		if !e.OccurredAt().Equal(occurred) || !e.CreatedAt().Equal(testNow) || e.EventID() != "ev-9" {
			t.Errorf("occurred=%v created=%v event=%q", e.OccurredAt(), e.CreatedAt(), e.EventID())
		}
	})

	t.Run("malformed action rejected", func(t *testing.T) {
		uc := NewRecordActionUseCase(&fakeRepo{}, newHasher(), newClock(), fakeTx{})
		if err := uc.Execute(context.Background(), RecordActionCmd{Action: "Not Valid", ResourceType: "tenant"}); err == nil {
			t.Fatal("expected error")
		}
	})
}
