package domain

import (
	"errors"
	"testing"
	"time"
)

func validApiKey(t *testing.T) *ApiKey {
	t.Helper()
	k, err := NewApiKey(
		NewApiKeyID(), NewTenantID(), "WooCommerce",
		"Xk3mPQrS", "hash-value", EnvironmentTest,
		[]Scope{ScopePaymentsWrite, ScopePaymentsRead},
	)
	if err != nil {
		t.Fatalf("unexpected error building valid api key: %v", err)
	}
	return k
}

func TestNewApiKeyValidations(t *testing.T) {
	scopes := []Scope{ScopePaymentsRead}
	tests := []struct {
		name    string
		keyName string
		prefix  string
		hash    string
		scopes  []Scope
	}{
		{"empty name", "", "pre", "hash", scopes},
		{"empty prefix", "name", "", "hash", scopes},
		{"empty hash", "name", "pre", "", scopes},
		{"no scopes", "name", "pre", "hash", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewApiKey(NewApiKeyID(), NewTenantID(), tt.keyName, tt.prefix, tt.hash, EnvironmentTest, tt.scopes); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewApiKeyEmitsIssuedEvent(t *testing.T) {
	k := validApiKey(t)
	events := k.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	issued, ok := events[0].(ApiKeyIssuedEvent)
	if !ok {
		t.Fatalf("expected ApiKeyIssuedEvent, got %T", events[0])
	}
	if issued.Prefix != "Xk3mPQrS" || issued.Environment != "test" {
		t.Errorf("event payload mismatch: %+v", issued)
	}
}

func TestApiKeyRevoke(t *testing.T) {
	k := validApiKey(t)
	k.PullEvents()

	if k.IsRevoked() {
		t.Fatal("new key should not be revoked")
	}
	if err := k.Revoke(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !k.IsRevoked() {
		t.Fatal("key should be revoked")
	}
	if k.RevokedAt() == nil {
		t.Fatal("RevokedAt should be set")
	}
	events := k.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if _, ok := events[0].(ApiKeyRevokedEvent); !ok {
		t.Fatalf("expected ApiKeyRevokedEvent, got %T", events[0])
	}

	if err := k.Revoke(); !errors.Is(err, ErrApiKeyAlreadyRevoked) {
		t.Fatalf("expected ErrApiKeyAlreadyRevoked, got %v", err)
	}
}

func TestReconstituteApiKey(t *testing.T) {
	id := NewApiKeyID()
	tid := NewTenantID()
	revoked := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scopes := []Scope{ScopePaymentsRead}

	k := ReconstituteApiKey(id, tid, "name", "prefix8", "hash", EnvironmentProduction, scopes, &revoked, created)

	if k.ID() != id || k.TenantID() != tid || k.Name() != "name" {
		t.Error("identity fields not restored")
	}
	if k.Prefix() != "prefix8" || k.KeyHash() != "hash" || !k.Environment().IsProd() {
		t.Error("prefix/hash/env not restored")
	}
	if len(k.Scopes()) != 1 || k.Scopes()[0] != ScopePaymentsRead {
		t.Error("scopes not restored")
	}
	if !k.IsRevoked() || k.RevokedAt() == nil || !k.RevokedAt().Equal(revoked) {
		t.Error("revoked state not restored")
	}
	if !k.CreatedAt().Equal(created) {
		t.Error("createdAt not restored")
	}
	if len(k.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func TestApiKeyHasScope(t *testing.T) {
	k := validApiKey(t)
	if !k.HasScope(ScopePaymentsWrite) {
		t.Error("should have payments:write")
	}
	if !k.HasScope(ScopePaymentsRead) {
		t.Error("should have payments:read")
	}
	if k.HasScope(ScopeWebhooks) {
		t.Error("should not have webhooks:write")
	}
}

func TestParseRawApiKey(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		p, err := ParseRawApiKey("test_Xk3mPQrS_7fGhJ9kL")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Environment != "test" || p.Prefix != "Xk3mPQrS" || p.Secret != "7fGhJ9kL" {
			t.Errorf("parsed wrong: %+v", p)
		}
	})

	t.Run("secret may contain underscores", func(t *testing.T) {
		p, err := ParseRawApiKey("production_abc123_secret_with_underscores")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Environment != "production" || p.Prefix != "abc123" {
			t.Errorf("parsed wrong: %+v", p)
		}
		if p.Secret != "secret_with_underscores" {
			t.Errorf("secret = %q, want secret_with_underscores", p.Secret)
		}
	})

	tests := []struct {
		name  string
		input string
	}{
		{"no underscores", "abcdef"},
		{"only one underscore", "test_abc"},
		{"empty env", "_prefix_secret"},
		{"empty prefix", "test__secret"},
		{"empty secret", "test_prefix_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRawApiKey(tt.input); err == nil {
				t.Fatalf("expected error for %q", tt.input)
			}
		})
	}
}
