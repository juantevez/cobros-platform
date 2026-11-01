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
	if _, err := repo.FindByHash(context.Background(), "does-not-exist"); !errors.Is(err, application.ErrRefreshTokenNotFound) {
		t.Fatalf("expected ErrRefreshTokenNotFound, got %v", err)
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
	if ok, err := repo.Revoke(ctx, tok.ID, ""); err != nil || !ok {
		t.Fatalf("revoke = %v, %v", ok, err)
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
	if _, err := repo.Revoke(ctx, oldTok.ID, newTok.ID); err != nil {
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

	if ok, err := repo.Revoke(ctx, tok.ID, ""); err != nil || !ok {
		t.Fatalf("first revoke = %v, %v", ok, err)
	}
	// Revocar de nuevo no es error, pero informa que no revocó nada: el
	// refresh lo usa para detectar que otro request rotó el token antes.
	if ok, err := repo.Revoke(ctx, tok.ID, ""); err != nil || ok {
		t.Fatalf("second revoke = %v, %v; want false, nil", ok, err)
	}
}

func TestRefreshTokenRepo_RevokeAllForUser(t *testing.T) {
	pool := requireDB(t)
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "all@example.com")
	other := seedUser(t, pool, tenant.ID(), "other@example.com")
	a := makeRefreshToken(user.ID(), tenant.ID(), "a-"+uuid.NewString())
	b := makeRefreshToken(user.ID(), tenant.ID(), "b-"+uuid.NewString())
	keep := makeRefreshToken(other.ID(), tenant.ID(), "k-"+uuid.NewString())
	for _, tok := range []application.RefreshToken{a, b, keep} {
		if err := repo.Save(ctx, tok); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	if err := repo.RevokeAllForUser(ctx, user.ID()); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	for _, tok := range []application.RefreshToken{a, b} {
		if got, _ := repo.FindByHash(ctx, tok.TokenHash); !got.IsRevoked() {
			t.Errorf("token %s of the user should be revoked", tok.ID)
		}
	}
	if got, _ := repo.FindByHash(ctx, keep.TokenHash); got.IsRevoked() {
		t.Error("another user's token must stay valid")
	}
}
