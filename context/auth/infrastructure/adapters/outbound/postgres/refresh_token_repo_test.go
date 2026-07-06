package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func makeRefreshToken(userID domain.UserID, tenantID domain.TenantID, hash string) application.RefreshToken {
	now := time.Now().UTC()
	return application.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		TenantID:  tenantID,
		TokenHash: hash,
		IssuedAt:  now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}

func TestRefreshTokenRepo_SaveAndFindByHash(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "rt@example.com")
	tok := makeRefreshToken(user.ID(), tenant.ID(), "hash-"+uuid.NewString())

	if err := repo.Save(ctx, tok); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByHash(ctx, tok.TokenHash)
	if err != nil {
		t.Fatalf("find by hash: %v", err)
	}
	if got.ID != tok.ID || got.UserID != user.ID() || got.TenantID != tenant.ID() {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.IsRevoked() {
		t.Error("new token should not be revoked")
	}
	timesClose(t, got.ExpiresAt, tok.ExpiresAt)
}

func TestRefreshTokenRepo_FindByHash_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	if _, err := repo.FindByHash(context.Background(), "does-not-exist"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestRefreshTokenRepo_RevokeLogout(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "logout@example.com")
	tok := makeRefreshToken(user.ID(), tenant.ID(), "hash-"+uuid.NewString())
	if err := repo.Save(ctx, tok); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Logout: revocar sin sucesor.
	if err := repo.Revoke(ctx, tok.ID, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	got, _ := repo.FindByHash(ctx, tok.TokenHash)
	if !got.IsRevoked() {
		t.Error("token should be revoked")
	}
	if got.ReplacedBy != nil {
		t.Errorf("logout should not set replaced_by, got %v", *got.ReplacedBy)
	}
}

func TestRefreshTokenRepo_RevokeRotation(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "rotate@example.com")

	oldTok := makeRefreshToken(user.ID(), tenant.ID(), "old-"+uuid.NewString())
	newTok := makeRefreshToken(user.ID(), tenant.ID(), "new-"+uuid.NewString())
	if err := repo.Save(ctx, oldTok); err != nil {
		t.Fatalf("save old: %v", err)
	}
	if err := repo.Save(ctx, newTok); err != nil {
		t.Fatalf("save new: %v", err)
	}

	// Rotación: revocar el viejo registrando al sucesor.
	if err := repo.Revoke(ctx, oldTok.ID, newTok.ID); err != nil {
		t.Fatalf("revoke rotation: %v", err)
	}

	got, _ := repo.FindByHash(ctx, oldTok.TokenHash)
	if !got.IsRevoked() {
		t.Error("old token should be revoked")
	}
	if got.ReplacedBy == nil || *got.ReplacedBy != newTok.ID {
		t.Errorf("replaced_by = %v, want %s", got.ReplacedBy, newTok.ID)
	}
}

func TestRefreshTokenRepo_RevokeIdempotent(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "idem@example.com")
	tok := makeRefreshToken(user.ID(), tenant.ID(), "hash-"+uuid.NewString())
	if err := repo.Save(ctx, tok); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := repo.Revoke(ctx, tok.ID, ""); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	// Revocar de nuevo no es error (rowsAffected 0 → nil).
	if err := repo.Revoke(ctx, tok.ID, ""); err != nil {
		t.Fatalf("second revoke should be idempotent, got %v", err)
	}
}
