package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// sha256Hash es la función de hash real usada en los tests: reproduce la
// cadena tamper-evident de producción.
func sha256Hash(payload string) []byte {
	sum := sha256.Sum256([]byte(payload))
	return sum[:]
}

func TestNewAuditLogEntry(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("computes hash and stores fields", func(t *testing.T) {
		meta := map[string]string{"ip": "10.0.0.1"}
		e, err := NewAuditLogEntry(EntryInput{TenantID: "tenant-1", Actor: "user-42", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "user-42", Metadata: meta, CorrelationID: "corr-1"}, nil, createdAt, sha256Hash)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.TenantID() != "tenant-1" || e.Actor() != "user-42" {
			t.Errorf("tenant/actor mismatch: %+v", e)
		}
		if e.Action() != ActionLogin || e.ResourceType() != ResourceUser || e.ResourceID() != "user-42" {
			t.Errorf("action/resource mismatch: %+v", e)
		}
		if e.CorrelationID() != "corr-1" {
			t.Errorf("correlationID mismatch: %q", e.CorrelationID())
		}
		if e.Metadata()["ip"] != "10.0.0.1" {
			t.Errorf("metadata mismatch: %+v", e.Metadata())
		}
		if len(e.Hash()) != sha256.Size {
			t.Errorf("hash length = %d, want %d", len(e.Hash()), sha256.Size)
		}
		if e.ID() != 0 {
			t.Errorf("id should be unset before persistence, got %d", e.ID())
		}
	})

	t.Run("empty actor defaults to system", func(t *testing.T) {
		e, err := NewAuditLogEntry(EntryInput{Action: ActionTenantCreated, ResourceType: ResourceTenant, ResourceID: "tenant-1"}, nil, createdAt, sha256Hash)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Actor() != "system" {
			t.Errorf("actor = %q, want system", e.Actor())
		}
	})

	t.Run("createdAt normalized to UTC", func(t *testing.T) {
		loc := time.FixedZone("ART", -3*3600)
		local := time.Date(2026, 7, 6, 9, 0, 0, 0, loc)
		e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, local, sha256Hash)
		if e.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt not UTC: %v", e.CreatedAt().Location())
		}
		if !e.CreatedAt().Equal(local) {
			t.Errorf("createdAt instant changed: %v vs %v", e.CreatedAt(), local)
		}
	})

	t.Run("hash is deterministic for identical input", func(t *testing.T) {
		mk := func() *AuditLogEntry {
			e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", CorrelationID: "c"}, nil, createdAt, sha256Hash)
			return e
		}
		h1 := mk().HashHex()
		h2 := mk().HashHex()
		if h1 != h2 {
			t.Error("hash not deterministic")
		}
	})

	t.Run("different fields produce different hashes", func(t *testing.T) {
		base, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", CorrelationID: "c"}, nil, createdAt, sha256Hash)
		other, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogout, ResourceType: ResourceUser, ResourceID: "r", CorrelationID: "c"}, nil, createdAt, sha256Hash)
		if base.HashHex() == other.HashHex() {
			t.Error("distinct actions produced identical hash")
		}
	})

	t.Run("metadata and correlationID are covered by the hash", func(t *testing.T) {
		mk := func(meta map[string]string, corr string) string {
			e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", Metadata: meta, CorrelationID: corr}, nil, createdAt, sha256Hash)
			return e.HashHex()
		}
		base := mk(map[string]string{"x": "1"}, "corr-A")
		if base == mk(map[string]string{"x": "2"}, "corr-A") {
			t.Error("metadata change not detected")
		}
		if base == mk(map[string]string{"x": "1"}, "corr-B") {
			t.Error("correlationID change not detected")
		}
	})

	t.Run("metadata key order and nil vs empty do not change the hash", func(t *testing.T) {
		a, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", Metadata: map[string]string{"a": "1", "b": "2"}}, nil, createdAt, sha256Hash)
		b, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", Metadata: map[string]string{"b": "2", "a": "1"}}, nil, createdAt, sha256Hash)
		if a.HashHex() != b.HashHex() {
			t.Error("map iteration order leaked into the hash")
		}
		n, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, createdAt, sha256Hash)
		e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", Metadata: map[string]string{}}, nil, createdAt, sha256Hash)
		if n.HashHex() != e.HashHex() {
			t.Error("nil and empty metadata must hash the same (JSONB round trip)")
		}
	})

	t.Run("separator inside a field cannot collide two entries", func(t *testing.T) {
		// Con el viejo join por "|", ("x|y","z") y ("x","y|z") producían el mismo payload.
		a, _ := NewAuditLogEntry(EntryInput{TenantID: "x|y", Actor: "z", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, createdAt, sha256Hash)
		b, _ := NewAuditLogEntry(EntryInput{TenantID: "x", Actor: "y|z", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, createdAt, sha256Hash)
		if a.HashHex() == b.HashHex() {
			t.Error("distinct entries collided")
		}
	})
}

func TestNewAuditLogEntry_storagePrecision(t *testing.T) {
	// Regresión: el hash se calculaba con nanosegundos, PostgreSQL guarda
	// microsegundos y al releer la fila VerifyHash fallaba siempre.
	withNanos := time.Date(2026, 10, 27, 1, 1, 45, 852_809_123, time.UTC)
	e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, withNanos, sha256Hash)

	if got := e.CreatedAt().Nanosecond(); got != 852_809_000 {
		t.Fatalf("createdAt nanos = %d, want truncated to µs (852809000)", got)
	}
	// Simula la ida y vuelta por TIMESTAMPTZ (precisión de microsegundos).
	fromDB := ReconstituteAuditLogEntry(EntrySnapshot{ID: 1, TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", Hash: e.Hash(), CreatedAt: withNanos.Truncate(time.Microsecond)})
	if !fromDB.VerifyHash(sha256Hash) {
		t.Error("entry read back with µs precision must verify")
	}
}

func TestVerifyHash(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", CorrelationID: "c"}, nil, createdAt, sha256Hash)

	t.Run("valid hash verifies", func(t *testing.T) {
		if !e.VerifyHash(sha256Hash) {
			t.Error("expected hash to verify")
		}
	})

	t.Run("tampered field fails verification", func(t *testing.T) {
		tampered := ReconstituteAuditLogEntry(EntrySnapshot{ID: 1, TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "DIFFERENT", CorrelationID: "c", Hash: e.Hash(), CreatedAt: createdAt})
		if tampered.VerifyHash(sha256Hash) {
			t.Error("tampered resourceID should not verify")
		}
	})

	t.Run("different hash length fails", func(t *testing.T) {
		if e.VerifyHash(func(string) []byte { return []byte{0x01} }) {
			t.Error("mismatched length should fail")
		}
	})
}

func TestReconstituteAuditLogEntry(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prev := []byte{0xAA, 0xBB}
	hash := []byte{0xCC, 0xDD}

	e := ReconstituteAuditLogEntry(EntrySnapshot{ID: 99, TenantID: "tenant", Actor: "actor", Action: ActionEntryPosted, ResourceType: ResourceEntry, ResourceID: "entry-1", Metadata: map[string]string{"k": "v"}, CorrelationID: "corr", PrevHash: prev, Hash: hash, CreatedAt: createdAt})

	if e.ID() != 99 || e.TenantID() != "tenant" || e.Actor() != "actor" {
		t.Errorf("scalar fields not restored: %+v", e)
	}
	if e.Action() != ActionEntryPosted || e.ResourceType() != ResourceEntry || e.ResourceID() != "entry-1" {
		t.Errorf("action/resource not restored")
	}
	if e.HashHex() != hex.EncodeToString(hash) || e.PrevHashHex() != hex.EncodeToString(prev) {
		t.Errorf("hashes not restored")
	}
	if !e.CreatedAt().Equal(createdAt) {
		t.Errorf("createdAt not restored")
	}
}

func TestHashHexAndPrevHashHex(t *testing.T) {
	t.Run("empty hashes return empty string", func(t *testing.T) {
		e := ReconstituteAuditLogEntry(EntrySnapshot{ID: 1, TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", CreatedAt: time.Now()})
		if e.HashHex() != "" {
			t.Errorf("HashHex = %q, want empty", e.HashHex())
		}
		if e.PrevHashHex() != "" {
			t.Errorf("PrevHashHex = %q, want empty", e.PrevHashHex())
		}
	})

	t.Run("non-empty encoded as hex", func(t *testing.T) {
		e := ReconstituteAuditLogEntry(EntrySnapshot{ID: 1, TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r", PrevHash: []byte{0x01, 0xFF}, Hash: []byte{0xDE, 0xAD}, CreatedAt: time.Now()})
		if e.HashHex() != "dead" {
			t.Errorf("HashHex = %q", e.HashHex())
		}
		if e.PrevHashHex() != "01ff" {
			t.Errorf("PrevHashHex = %q", e.PrevHashHex())
		}
	})
}

func TestChainLinksTo(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	first, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionTenantCreated, ResourceType: ResourceTenant, ResourceID: "t1"}, nil, createdAt, sha256Hash)
	second, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "u1"}, first.Hash(), createdAt, sha256Hash)

	t.Run("first entry links to nil", func(t *testing.T) {
		if !first.ChainLinksTo(nil) {
			t.Error("first entry with no prevHash should link to nil")
		}
	})

	t.Run("non-first entry does not link to nil", func(t *testing.T) {
		if second.ChainLinksTo(nil) {
			t.Error("entry with prevHash should not link to nil")
		}
	})

	t.Run("valid successor links to predecessor", func(t *testing.T) {
		if !second.ChainLinksTo(first) {
			t.Error("second should link to first")
		}
	})

	t.Run("broken link detected", func(t *testing.T) {
		unrelated, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogout, ResourceType: ResourceUser, ResourceID: "u2"}, nil, createdAt, sha256Hash)
		if second.ChainLinksTo(unrelated) {
			t.Error("second should not link to an unrelated entry")
		}
	})

	t.Run("mismatched prevHash length detected", func(t *testing.T) {
		badPrev := ReconstituteAuditLogEntry(EntrySnapshot{ID: 2, TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "u1", PrevHash: []byte{0x01}, Hash: second.Hash(), CreatedAt: createdAt})
		if badPrev.ChainLinksTo(first) {
			t.Error("length mismatch should break the link")
		}
	})
}

func TestString(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	e, _ := NewAuditLogEntry(EntryInput{TenantID: "t", Actor: "a", Action: ActionLogin, ResourceType: ResourceUser, ResourceID: "r"}, nil, createdAt, sha256Hash)
	s := e.String()
	if s == "" {
		t.Fatal("String() returned empty")
	}
	// El resumen incluye acción y recurso.
	for _, want := range []string{"auth.user.login", "user/r"} {
		if !contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
