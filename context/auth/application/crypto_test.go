package application

import (
	"strings"
	"testing"
)

func TestGenerateSecret(t *testing.T) {
	const n = 32
	s, err := generateSecret(n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s) != n {
		t.Errorf("length = %d, want %d", len(s), n)
	}
	for _, c := range s {
		if !strings.ContainsRune(base62Alphabet, c) {
			t.Errorf("char %q not in base62 alphabet", c)
		}
	}
}

func TestGenerateSecret_IsRandom(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		s, err := generateSecret(16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seen[s] {
			t.Fatalf("generated duplicate secret: %q", s)
		}
		seen[s] = true
	}
}

func TestGenerateSecret_ZeroLength(t *testing.T) {
	s, err := generateSecret(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}
