package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSHA256Hasher_Compute(t *testing.T) {
	h := NewSHA256Hasher()

	t.Run("matches standard sha256", func(t *testing.T) {
		payload := "prev|tenant|actor|auth.user.login|user|u1|2026-07-06T12:00:00Z"
		got := h.Compute(payload)
		want := sha256.Sum256([]byte(payload))
		if hex.EncodeToString(got) != hex.EncodeToString(want[:]) {
			t.Errorf("digest mismatch:\n got=%x\nwant=%x", got, want[:])
		}
	})

	t.Run("returns 32 bytes", func(t *testing.T) {
		if n := len(h.Compute("anything")); n != sha256.Size {
			t.Errorf("len = %d, want %d", n, sha256.Size)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		a := h.Compute("same input")
		b := h.Compute("same input")
		if hex.EncodeToString(a) != hex.EncodeToString(b) {
			t.Error("hash not deterministic for identical input")
		}
	})

	t.Run("different input yields different digest", func(t *testing.T) {
		if hex.EncodeToString(h.Compute("a")) == hex.EncodeToString(h.Compute("b")) {
			t.Error("distinct inputs produced identical digest")
		}
	})

	t.Run("empty payload hashes to sha256 of empty string", func(t *testing.T) {
		got := h.Compute("")
		want := sha256.Sum256([]byte(""))
		if hex.EncodeToString(got) != hex.EncodeToString(want[:]) {
			t.Errorf("empty digest mismatch: got=%x", got)
		}
	})
}
