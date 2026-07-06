package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func makeApiKey(t *testing.T, tenantID domain.TenantID, prefix string) *domain.ApiKey {
	t.Helper()
	k, err := domain.NewApiKey(
		domain.NewApiKeyID(), tenantID, "integration", prefix, "argon2:keyhash",
		domain.EnvironmentProduction, []domain.Scope{domain.ScopePaymentsWrite, domain.ScopePaymentsRead},
	)
	if err != nil {
		t.Fatalf("build api key: %v", err)
	}
	k.PullEvents()
	return k
}

// uniquePrefix genera un prefix único (la columna prefix es UNIQUE global).
func uniquePrefix() string {
	return fmt.Sprintf("p%s", domain.NewApiKeyID().String()[:7])
}

func TestApiKeyRepo_SaveAndFind(t *testing.T) {
	pool := requireDB(t)
	repo := NewApiKeyRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	prefix := uniquePrefix()
	key := makeApiKey(t, tenant.ID(), prefix)

	if err := repo.Save(ctx, key); err != nil {
		t.Fatalf("save: %v", err)
	}

	t.Run("find by id restores scopes", func(t *testing.T) {
		got, err := repo.FindByID(ctx, key.ID())
		if err != nil {
			t.Fatalf("find by id: %v", err)
		}
		if got.Prefix() != prefix || got.KeyHash() != "argon2:keyhash" {
			t.Errorf("mismatch: prefix=%s hash=%s", got.Prefix(), got.KeyHash())
		}
		if !got.Environment().IsProd() {
			t.Errorf("env = %s, want production", got.Environment())
		}
		if !got.HasScope(domain.ScopePaymentsWrite) || !got.HasScope(domain.ScopePaymentsRead) {
			t.Errorf("scopes not restored: %v", got.Scopes())
		}
		if got.IsRevoked() {
			t.Error("new key should not be revoked")
		}
	})

	t.Run("find by prefix", func(t *testing.T) {
		got, err := repo.FindByPrefix(ctx, prefix)
		if err != nil {
			t.Fatalf("find by prefix: %v", err)
		}
		if got.ID() != key.ID() {
			t.Errorf("wrong key: %s", got.ID())
		}
	})
}

func TestApiKeyRepo_Revoke(t *testing.T) {
	pool := requireDB(t)
	repo := NewApiKeyRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	key := makeApiKey(t, tenant.ID(), uniquePrefix())
	if err := repo.Save(ctx, key); err != nil {
		t.Fatalf("save: %v", err)
	}

	_ = key.Revoke()
	if err := repo.Update(ctx, key); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.FindByID(ctx, key.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !got.IsRevoked() || got.RevokedAt() == nil {
		t.Error("revoked_at not persisted")
	}
}

func TestApiKeyRepo_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewApiKeyRepository(pool)
	ctx := context.Background()

	if _, err := repo.FindByID(ctx, domain.NewApiKeyID()); !errors.Is(err, domain.ErrApiKeyNotFound) {
		t.Fatalf("find by id: expected ErrApiKeyNotFound, got %v", err)
	}
	if _, err := repo.FindByPrefix(ctx, "nonexistent-prefix"); !errors.Is(err, domain.ErrApiKeyNotFound) {
		t.Fatalf("find by prefix: expected ErrApiKeyNotFound, got %v", err)
	}
}

func TestApiKeyRepo_Update_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewApiKeyRepository(pool)

	key := makeApiKey(t, domain.NewTenantID(), uniquePrefix())
	if err := repo.Update(context.Background(), key); !errors.Is(err, domain.ErrApiKeyNotFound) {
		t.Fatalf("expected ErrApiKeyNotFound, got %v", err)
	}
}
