package application

import (
	"context"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// validChain construye una cadena encadenada de n entradas.
func validChain(n int) []*domain.AuditLogEntry {
	entries := make([]*domain.AuditLogEntry, 0, n)
	var prevHash []byte
	for i := 0; i < n; i++ {
		e := buildEntry("t", domain.ActionLogin, domain.ResourceUser, "u", prevHash)
		entries = append(entries, e)
		prevHash = e.Hash()
	}
	return entries
}

// withIDs reconstituye las entradas con IDs consecutivos desde first (como
// las devolvería la base).
func withIDs(entries []*domain.AuditLogEntry, first int64) []*domain.AuditLogEntry {
	out := make([]*domain.AuditLogEntry, len(entries))
	for i, e := range entries {
		out[i] = domain.ReconstituteAuditLogEntry(domain.EntrySnapshot{
			ID: first + int64(i), TenantID: e.TenantID(), Actor: e.Actor(), Action: e.Action(),
			ResourceType: e.ResourceType(), ResourceID: e.ResourceID(), Metadata: e.Metadata(),
			CorrelationID: e.CorrelationID(), EventID: e.EventID(), OccurredAt: e.OccurredAt(),
			PrevHash: e.PrevHash(), Hash: e.Hash(), CreatedAt: e.CreatedAt(),
		})
	}
	return out
}

func TestVerifyChainUseCase_Execute(t *testing.T) {
	t.Run("valid chain from genesis", func(t *testing.T) {
		repo := &fakeRepo{fromID: validChain(3)}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid, got %+v", res)
		}
		if res.EntriesChecked != 3 {
			t.Errorf("EntriesChecked = %d, want 3", res.EntriesChecked)
		}
	})

	t.Run("empty chain is valid", func(t *testing.T) {
		repo := &fakeRepo{fromID: nil}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Valid || res.EntriesChecked != 0 {
			t.Errorf("unexpected result: %+v", res)
		}
	})

	t.Run("default batch size used when limit unset", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())
		if _, err := uc.Execute(context.Background(), VerifyChainQuery{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != verifyBatchSize {
			t.Errorf("limit = %d, want %d", repo.lastLimit, verifyBatchSize)
		}
	})

	t.Run("explicit limit and fromID forwarded", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())
		if _, err := uc.Execute(context.Background(), VerifyChainQuery{FromID: 42, Limit: 10}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastFromID != 42 || repo.lastLimit != 10 {
			t.Errorf("fromID=%d limit=%d", repo.lastFromID, repo.lastLimit)
		}
	})

	t.Run("tampered hash detected", func(t *testing.T) {
		chain := validChain(3)
		// Reconstituir la entrada del medio con un resourceID distinto al que
		// generó su hash: VerifyHash debe fallar.
		bad := domain.ReconstituteAuditLogEntry(domain.EntrySnapshot{ID: 2, TenantID: "t", Actor: "actor", Action: domain.ActionLogin, ResourceType: domain.ResourceUser, ResourceID: "TAMPERED", PrevHash: chain[1].PrevHash(), Hash: chain[1].Hash(), CreatedAt: testNow})
		chain[1] = bad
		repo := &fakeRepo{fromID: chain}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Valid {
			t.Fatal("expected invalid result")
		}
		if res.FirstInvalidID != 2 || res.EntriesChecked != 2 {
			t.Errorf("unexpected result: %+v", res)
		}
	})

	t.Run("broken link detected", func(t *testing.T) {
		chain := validChain(3)
		// Rehacer la entrada 2 con un prevHash que no corresponde a la entrada 1.
		// Su propio hash sigue siendo válido (VerifyHash pasa) pero el enlace no.
		broken := buildEntry("t", domain.ActionLogin, domain.ResourceUser, "u", []byte("wrong-prev-hash-of-32-bytes-len!"))
		chain[2] = broken
		repo := &fakeRepo{fromID: chain}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Valid {
			t.Fatal("expected invalid result")
		}
		if res.EntriesChecked != 3 {
			t.Errorf("EntriesChecked = %d, want 3", res.EntriesChecked)
		}
	})

	t.Run("genesis entry with unexpected prev_hash rejected", func(t *testing.T) {
		// Primera entrada de la cadena (FromID=0) pero con prevHash presente.
		first := buildEntry("t", domain.ActionLogin, domain.ResourceUser, "u", []byte("unexpected-prev-hash-32-bytes!!!"))
		repo := &fakeRepo{fromID: []*domain.AuditLogEntry{first}}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{FromID: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Valid {
			t.Fatal("expected invalid result")
		}
		if res.EntriesChecked != 1 {
			t.Errorf("EntriesChecked = %d, want 1", res.EntriesChecked)
		}
	})

	t.Run("batch from a non-zero FromID is linked to its predecessor", func(t *testing.T) {
		chain := withIDs(validChain(4), 1)
		repo := &fakeRepo{fromID: chain[2:], before: chain[1]}
		res, err := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{FromID: 3})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Valid || repo.lastBeforeID != 3 {
			t.Errorf("res = %+v, FindBefore(%d)", res, repo.lastBeforeID)
		}
	})

	t.Run("broken link at the batch border is detected", func(t *testing.T) {
		chain := withIDs(validChain(4), 1)
		repo := &fakeRepo{fromID: chain[2:], before: chain[0]} // la predecesora real (id 2) no es esta
		res, _ := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{FromID: 3})
		if res.Valid || res.FirstInvalidID != 3 {
			t.Errorf("border break not detected: %+v", res)
		}
	})

	t.Run("batch without predecessor must start at genesis", func(t *testing.T) {
		chain := withIDs(validChain(3), 1)
		repo := &fakeRepo{fromID: chain[1:]} // falta la génesis: FindBefore no encuentra nada
		res, _ := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{FromID: 2})
		if res.Valid || res.FirstInvalidID != 2 {
			t.Errorf("missing genesis not detected: %+v", res)
		}
	})

	t.Run("pagination: full batch is incomplete and points to the next id", func(t *testing.T) {
		chain := withIDs(validChain(3), 10)
		repo := &fakeRepo{fromID: chain}
		res, _ := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{Limit: 3})
		if !res.Valid || res.Complete || res.NextFromID != 13 {
			t.Errorf("res = %+v, want incomplete with next_from_id 13", res)
		}
		repo = &fakeRepo{fromID: chain}
		res, _ = NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{Limit: 10})
		if !res.Complete || res.NextFromID != 0 {
			t.Errorf("res = %+v, want complete", res)
		}
	})

	t.Run("limit is capped", func(t *testing.T) {
		repo := &fakeRepo{}
		_, _ = NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{Limit: 1_000_000})
		if repo.lastLimit != maxVerifyBatchSize {
			t.Errorf("limit = %d, want %d", repo.lastLimit, maxVerifyBatchSize)
		}
	})

	t.Run("predecessor lookup error propagated", func(t *testing.T) {
		repo := &fakeRepo{fromID: validChain(1), beforeErr: errBoom}
		if _, err := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher()).Execute(context.Background(), VerifyChainQuery{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		repo := &fakeRepo{fromIDErr: errBoom}
		uc := NewVerifyChainUseCase(repo, &fakeCheckpoints{}, newHasher())
		if _, err := uc.Execute(context.Background(), VerifyChainQuery{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("checkpoint whose entry is missing: tail truncated", func(t *testing.T) {
		repo := &fakeRepo{fromID: validChain(2)}
		cps := &fakeCheckpoints{mismatch: &domain.CheckpointMismatch{CheckpointID: 7, EntryID: 42, EntryMissing: true}}
		res, err := NewVerifyChainUseCase(repo, cps, newHasher()).Execute(context.Background(), VerifyChainQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Valid || res.FirstInvalidID != 42 || !strings.Contains(res.FirstInvalidMsg, "truncated") {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("checkpoint whose entry hash changed: chain rewritten", func(t *testing.T) {
		cps := &fakeCheckpoints{mismatch: &domain.CheckpointMismatch{CheckpointID: 7, EntryID: 42}}
		res, _ := NewVerifyChainUseCase(&fakeRepo{}, cps, newHasher()).Execute(context.Background(), VerifyChainQuery{})
		if res.Valid || !strings.Contains(res.FirstInvalidMsg, "rewritten") {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("checkpoint lookup error propagated", func(t *testing.T) {
		cps := &fakeCheckpoints{mismatchErr: errBoom}
		if _, err := NewVerifyChainUseCase(&fakeRepo{}, cps, newHasher()).Execute(context.Background(), VerifyChainQuery{}); err == nil {
			t.Fatal("expected error")
		}
	})
}
