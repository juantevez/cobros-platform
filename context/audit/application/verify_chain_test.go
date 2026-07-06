package application

import (
	"context"
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

func TestVerifyChainUseCase_Execute(t *testing.T) {
	t.Run("valid chain from genesis", func(t *testing.T) {
		repo := &fakeRepo{fromID: validChain(3)}
		uc := NewVerifyChainUseCase(repo, newHasher())

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
		uc := NewVerifyChainUseCase(repo, newHasher())

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
		uc := NewVerifyChainUseCase(repo, newHasher())
		if _, err := uc.Execute(context.Background(), VerifyChainQuery{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != verifyBatchSize {
			t.Errorf("limit = %d, want %d", repo.lastLimit, verifyBatchSize)
		}
	})

	t.Run("explicit limit and fromID forwarded", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewVerifyChainUseCase(repo, newHasher())
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
		bad := domain.ReconstituteAuditLogEntry(
			2, "t", "actor", domain.ActionLogin, domain.ResourceUser, "TAMPERED",
			nil, "", chain[1].PrevHash(), chain[1].Hash(), testNow,
		)
		chain[1] = bad
		repo := &fakeRepo{fromID: chain}
		uc := NewVerifyChainUseCase(repo, newHasher())

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
		uc := NewVerifyChainUseCase(repo, newHasher())

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
		uc := NewVerifyChainUseCase(repo, newHasher())

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

	t.Run("partial verification from non-zero FromID skips genesis check", func(t *testing.T) {
		// Con FromID != 0 la primera entrada puede legítimamente tener prevHash.
		e := buildEntry("t", domain.ActionLogin, domain.ResourceUser, "u", []byte("some-prev-hash-of-32-bytes-len!!"))
		repo := &fakeRepo{fromID: []*domain.AuditLogEntry{e}}
		uc := NewVerifyChainUseCase(repo, newHasher())

		res, err := uc.Execute(context.Background(), VerifyChainQuery{FromID: 100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid, got %+v", res)
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		repo := &fakeRepo{fromIDErr: errBoom}
		uc := NewVerifyChainUseCase(repo, newHasher())
		if _, err := uc.Execute(context.Background(), VerifyChainQuery{}); err == nil {
			t.Fatal("expected error")
		}
	})
}
