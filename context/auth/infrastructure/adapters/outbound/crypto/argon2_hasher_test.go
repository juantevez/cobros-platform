package crypto

import (
	"strings"
	"testing"
)

func TestHashVerify_RoundTrip(t *testing.T) {
	h := NewArgon2Hasher()

	encoded, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := h.Verify("correct horse battery staple", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("correct password should verify")
	}

	ok, err = h.Verify("wrong password", encoded)
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if ok {
		t.Error("wrong password must not verify")
	}
}

func TestHash_PHCFormat(t *testing.T) {
	h := NewArgon2Hasher()
	encoded, err := h.Hash("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("unexpected PHC prefix: %q", encoded)
	}
	if parts := strings.Split(encoded, "$"); len(parts) != 6 {
		t.Errorf("expected 6 PHC parts, got %d", len(parts))
	}
}

func TestHash_IsSalted(t *testing.T) {
	h := NewArgon2Hasher()
	a, _ := h.Hash("same")
	b, _ := h.Hash("same")
	if a == b {
		t.Error("two hashes of the same password must differ (random salt)")
	}
	// Pero ambos deben verificar.
	if ok, _ := h.Verify("same", a); !ok {
		t.Error("hash a should verify")
	}
	if ok, _ := h.Verify("same", b); !ok {
		t.Error("hash b should verify")
	}
}

func TestHashVerify_EmptyPassword(t *testing.T) {
	h := NewArgon2Hasher()
	encoded, err := h.Hash("")
	if err != nil {
		t.Fatalf("hash empty: %v", err)
	}
	if ok, _ := h.Verify("", encoded); !ok {
		t.Error("empty password should verify against its own hash")
	}
	if ok, _ := h.Verify("x", encoded); ok {
		t.Error("non-empty password must not verify against empty hash")
	}
}

func TestVerify_MalformedHash(t *testing.T) {
	h := NewArgon2Hasher()
	// salt "c2FsdA" = "salt", hash "aGFzaA" = "hash" (RawStdEncoding válidos).
	tests := []struct {
		name    string
		encoded string
	}{
		{"not phc", "plain-text-not-a-hash"},
		{"too few parts", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA"},
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"bad version field", "$argon2id$version$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"incompatible version", "$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"bad cost params", "$argon2id$v=19$mmm$c2FsdA$aGFzaA"},
		{"bad salt base64", "$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA"},
		{"bad hash base64", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := h.Verify("pw", tt.encoded)
			if err == nil {
				t.Fatalf("expected an error for %q", tt.encoded)
			}
			if ok {
				t.Error("malformed hash must not verify as true")
			}
		})
	}
}

func TestVerify_CrossHasherCompatible(t *testing.T) {
	// Un hash producido por una instancia debe verificar en otra
	// (los parámetros de coste viajan dentro del propio hash PHC).
	encoded, err := NewArgon2Hasher().Hash("portable")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if ok, err := NewArgon2Hasher().Verify("portable", encoded); err != nil || !ok {
		t.Fatalf("cross-instance verify failed: ok=%v err=%v", ok, err)
	}
}
