package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestIssueApiKey_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	apiRepo := newFakeApiKeyRepo()
	hasher := &fakeHasher{}
	pub := &fakePublisher{}
	uc := NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), apiRepo, hasher, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), IssueApiKeyCmd{
		TenantID:    tenant.ID().String(),
		Name:        "WooCommerce",
		Environment: "production",
		Scopes:      []string{"payments:write", "payments:read"},
		IssuedBy:    domain.NewUserID().String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ApiKeyID == "" || res.FullKey == "" {
		t.Fatal("result missing id or full key")
	}
	if len(res.Prefix) != prefixLength {
		t.Errorf("prefix length = %d, want %d", len(res.Prefix), prefixLength)
	}
	// La FullKey debe reparsearse a env/prefix consistentes.
	parsed, err := domain.ParseRawApiKey(res.FullKey)
	if err != nil {
		t.Fatalf("full key not parseable: %v", err)
	}
	if parsed.Environment != "production" || parsed.Prefix != res.Prefix {
		t.Errorf("parsed key mismatch: %+v", parsed)
	}
	// Se persiste el hash del secreto, no el secreto.
	if apiRepo.saved == nil {
		t.Fatal("api key not saved")
	}
	if apiRepo.saved.KeyHash() != "hash:"+parsed.Secret {
		t.Errorf("stored hash = %q, want hash of secret", apiRepo.saved.KeyHash())
	}
	// Evento emitido.
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.ApiKeyIssuedEvent); !ok {
		t.Fatalf("expected ApiKeyIssuedEvent, got %T", pub.published[0])
	}
}

func TestIssueApiKey_TenantNotActive(t *testing.T) {
	tenant := newPendingTenant(t)
	uc := NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), newFakeApiKeyRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), IssueApiKeyCmd{
		TenantID: tenant.ID().String(), Name: "k", Environment: "test",
		Scopes: []string{"payments:read"},
	})
	if !errors.Is(err, domain.ErrTenantNotActive) {
		t.Fatalf("expected ErrTenantNotActive, got %v", err)
	}
}

func TestIssueApiKey_ProductionKeyOnTestTenant(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentTest) // activo pero en test
	uc := NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), newFakeApiKeyRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), IssueApiKeyCmd{
		TenantID: tenant.ID().String(), Name: "k", Environment: "production",
		Scopes: []string{"payments:read"},
	})
	if !errors.Is(err, domain.ErrTenantNotInProduction) {
		t.Fatalf("expected ErrTenantNotInProduction, got %v", err)
	}
}

func TestIssueApiKey_ValidationErrors(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	newUC := func() *IssueApiKeyUseCase {
		return NewIssueApiKeyUseCase(newFakeTenantRepo(tenant), newFakeApiKeyRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	}

	t.Run("invalid environment", func(t *testing.T) {
		_, err := newUC().Execute(context.Background(), IssueApiKeyCmd{
			TenantID: tenant.ID().String(), Name: "k", Environment: "staging", Scopes: []string{"payments:read"},
		})
		if !errors.Is(err, domain.ErrInvalidEnvironment) {
			t.Fatalf("expected ErrInvalidEnvironment, got %v", err)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		_, err := newUC().Execute(context.Background(), IssueApiKeyCmd{
			TenantID: tenant.ID().String(), Name: "", Environment: "production", Scopes: []string{"payments:read"},
		})
		if !errors.Is(err, domain.ErrApiKeyNameRequired) {
			t.Fatalf("expected ErrApiKeyNameRequired, got %v", err)
		}
	})

	t.Run("invalid scope", func(t *testing.T) {
		_, err := newUC().Execute(context.Background(), IssueApiKeyCmd{
			TenantID: tenant.ID().String(), Name: "k", Environment: "production", Scopes: []string{"admin:all"},
		})
		if !errors.Is(err, domain.ErrInvalidScope) {
			t.Fatalf("expected ErrInvalidScope, got %v", err)
		}
	})

	t.Run("no scopes", func(t *testing.T) {
		_, err := newUC().Execute(context.Background(), IssueApiKeyCmd{
			TenantID: tenant.ID().String(), Name: "k", Environment: "production", Scopes: nil,
		})
		if !errors.Is(err, domain.ErrApiKeyScopesRequired) {
			t.Fatalf("expected ErrApiKeyScopesRequired, got %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := newUC().Execute(context.Background(), IssueApiKeyCmd{
			TenantID: "nope", Name: "k", Environment: "production", Scopes: []string{"payments:read"},
		})
		if !errors.Is(err, domain.ErrInvalidID) {
			t.Fatalf("expected ErrInvalidID, got %v", err)
		}
	})
}
