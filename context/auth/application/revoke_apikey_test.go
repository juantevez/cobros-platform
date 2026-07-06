package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRevokeApiKey_Success(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID)
	repo := newFakeApiKeyRepo()
	repo.byID[key.ID()] = key
	pub := &fakePublisher{}
	uc := NewRevokeApiKeyUseCase(repo, fakeTx{}, pub)

	err := uc.Execute(context.Background(), RevokeApiKeyCmd{
		TenantID: tenantID.String(),
		ApiKeyID: key.ID().String(),
		RevokedBy: domain.NewUserID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.byID[key.ID()].IsRevoked() {
		t.Error("key should be revoked")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.ApiKeyRevokedEvent); !ok {
		t.Fatalf("expected ApiKeyRevokedEvent, got %T", pub.published[0])
	}
}

func TestRevokeApiKey_InvalidIDs(t *testing.T) {
	uc := NewRevokeApiKeyUseCase(newFakeApiKeyRepo(), fakeTx{}, &fakePublisher{})

	t.Run("invalid tenant id", func(t *testing.T) {
		err := uc.Execute(context.Background(), RevokeApiKeyCmd{TenantID: "nope", ApiKeyID: domain.NewApiKeyID().String()})
		if !errors.Is(err, domain.ErrInvalidID) {
			t.Fatalf("expected ErrInvalidID, got %v", err)
		}
	})
	t.Run("invalid api key id", func(t *testing.T) {
		err := uc.Execute(context.Background(), RevokeApiKeyCmd{TenantID: domain.NewTenantID().String(), ApiKeyID: "nope"})
		if !errors.Is(err, domain.ErrInvalidID) {
			t.Fatalf("expected ErrInvalidID, got %v", err)
		}
	})
}

func TestRevokeApiKey_NotFound(t *testing.T) {
	uc := NewRevokeApiKeyUseCase(newFakeApiKeyRepo(), fakeTx{}, &fakePublisher{})
	err := uc.Execute(context.Background(), RevokeApiKeyCmd{
		TenantID: domain.NewTenantID().String(), ApiKeyID: domain.NewApiKeyID().String(),
	})
	if !errors.Is(err, domain.ErrApiKeyNotFound) {
		t.Fatalf("expected ErrApiKeyNotFound, got %v", err)
	}
}

func TestRevokeApiKey_WrongTenant(t *testing.T) {
	key := newApiKey(t, domain.NewTenantID()) // pertenece a un tenant
	repo := newFakeApiKeyRepo()
	repo.byID[key.ID()] = key
	uc := NewRevokeApiKeyUseCase(repo, fakeTx{}, &fakePublisher{})

	// Otro tenant intenta revocarla → ErrApiKeyNotFound (no revela existencia).
	err := uc.Execute(context.Background(), RevokeApiKeyCmd{
		TenantID: domain.NewTenantID().String(), ApiKeyID: key.ID().String(),
	})
	if !errors.Is(err, domain.ErrApiKeyNotFound) {
		t.Fatalf("expected ErrApiKeyNotFound, got %v", err)
	}
}

func TestRevokeApiKey_AlreadyRevoked(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID)
	_ = key.Revoke()
	key.PullEvents()
	repo := newFakeApiKeyRepo()
	repo.byID[key.ID()] = key
	uc := NewRevokeApiKeyUseCase(repo, fakeTx{}, &fakePublisher{})

	err := uc.Execute(context.Background(), RevokeApiKeyCmd{
		TenantID: tenantID.String(), ApiKeyID: key.ID().String(),
	})
	if !errors.Is(err, domain.ErrApiKeyAlreadyRevoked) {
		t.Fatalf("expected ErrApiKeyAlreadyRevoked, got %v", err)
	}
}
