package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestHexSecretGenerator_Generate(t *testing.T) {
	g := NewHexSecretGenerator()
	seen := map[string]bool{}

	for i := 0; i < 100; i++ {
		s, err := g.Generate()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(s, "whsec_") || len(s) != len("whsec_")+64 {
			t.Fatalf("secret %q: want whsec_ + 64 hex chars", s)
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(s, "whsec_"))
		if err != nil || len(raw) != 32 {
			t.Fatalf("secret %q is not 32 bytes of hex: %v", s, err)
		}
		if seen[s] {
			t.Fatalf("duplicate secret generated: %q", s)
		}
		seen[s] = true
	}
}
