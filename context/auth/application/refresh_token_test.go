package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

var refreshNow = time.Unix(1_700_000_000, 0).UTC()

// storedToken construye un refresh token persistido para el usuario/tenant dados,
// accesible por el hash "hash:<raw>" (según el fakeHasher).
func storedToken(id, raw string, user *domain.User, tenant *domain.Tenant, expires time.Time, revoked bool) (*RefreshToken, map[string]*RefreshToken) {
	rt := &RefreshToken{
		ID:        id,
		UserID:    user.ID(),
		TenantID:  tenant.ID(),
		TokenHash: "hash:" + raw,
		IssuedAt:  refreshNow.Add(-time.Minute),
		ExpiresAt: expires,
	}
	if revoked {
		r := refreshNow.Add(-time.Minute)
		rt.RevokedAt = &r
	}
	return rt, map[string]*RefreshToken{rt.TokenHash: rt}
}

func newRefreshUC(
	user *domain.User, tenant *domain.Tenant, memberships []domain.Membership,
	refreshRepo *fakeRefreshRepo, issuer *fakeTokenIssuer,
) *RefreshTokenUseCase {
	userRepo := newFakeUserRepo()
	if user != nil {
		userRepo.byID[user.ID()] = user
	}
	tenantRepo := newFakeTenantRepo()
	if tenant != nil {
		tenantRepo.tenants[tenant.ID()] = tenant
	}
	return NewRefreshTokenUseCase(
		userRepo, newFakeMembershipRepo(memberships...), tenantRepo,
		refreshRepo, &fakeHasher{}, issuer, fakeClock{now: refreshNow},
	)
}

func TestRefreshToken_SuccessRotates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleAccountant, domain.NewUserID())

	stored, byHash := storedToken("old-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), false)
	refreshRepo := &fakeRefreshRepo{byHash: byHash}
	issuer := &fakeTokenIssuer{access: "new-access", refresh: "new-raw"}
	uc := newRefreshUC(user, tenant, []domain.Membership{membership}, refreshRepo, issuer)

	pair, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.AccessToken != "new-access" || pair.RefreshToken != "new-raw" {
		t.Errorf("unexpected token pair: %+v", pair)
	}
	// Claims con el rol actualizado.
	if issuer.gotClaims.Role != domain.RoleAccountant {
		t.Errorf("claims role = %q, want accountant", issuer.gotClaims.Role)
	}
	// Nuevo token persistido, hasheado.
	if refreshRepo.saved == nil || refreshRepo.saved.TokenHash != "hash:new-raw" {
		t.Fatalf("new refresh token not persisted hashed: %+v", refreshRepo.saved)
	}
	// El token viejo se revoca registrando al sucesor.
	if !refreshRepo.revokeCalled {
		t.Fatal("old token was not revoked")
	}
	if refreshRepo.revokedID != stored.ID {
		t.Errorf("revoked id = %q, want %q", refreshRepo.revokedID, stored.ID)
	}
	if refreshRepo.revokedByID != refreshRepo.saved.ID {
		t.Errorf("replacedBy = %q, want new token id %q", refreshRepo.revokedByID, refreshRepo.saved.ID)
	}
}

func TestRefreshToken_EmptyToken(t *testing.T) {
	uc := newRefreshUC(nil, nil, nil, &fakeRefreshRepo{}, &fakeTokenIssuer{})
	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: ""})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshToken_NotFound(t *testing.T) {
	uc := newRefreshUC(nil, nil, nil, &fakeRefreshRepo{}, &fakeTokenIssuer{})
	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshToken_Revoked(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	_, byHash := storedToken("old-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), true)
	refreshRepo := &fakeRefreshRepo{byHash: byHash}
	uc := newRefreshUC(user, tenant, nil, refreshRepo, &fakeTokenIssuer{})

	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshToken_Expired(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	_, byHash := storedToken("old-id", "raw-refresh", user, tenant, refreshNow.Add(-time.Hour), false)
	refreshRepo := &fakeRefreshRepo{byHash: byHash}
	uc := newRefreshUC(user, tenant, nil, refreshRepo, &fakeTokenIssuer{})

	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshToken_SuspendedUser(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	_ = user.Suspend()
	user.PullEvents()
	_, byHash := storedToken("old-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), false)
	uc := newRefreshUC(user, tenant, nil, &fakeRefreshRepo{byHash: byHash}, &fakeTokenIssuer{})

	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if !errors.Is(err, domain.ErrUserSuspended) {
		t.Fatalf("expected ErrUserSuspended, got %v", err)
	}
}

func TestRefreshToken_SuspendedTenant(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	_ = tenant.Suspend("fraud")
	tenant.PullEvents()
	_, byHash := storedToken("old-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), false)
	uc := newRefreshUC(user, tenant, nil, &fakeRefreshRepo{byHash: byHash}, &fakeTokenIssuer{})

	_, err := uc.Execute(context.Background(), RefreshTokenCmd{RawRefreshToken: "raw-refresh"})
	if !errors.Is(err, domain.ErrTenantSuspended) {
		t.Fatalf("expected ErrTenantSuspended, got %v", err)
	}
}

// ── Logout ────────────────────────────────────────────────────────────────────

func TestLogout_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	stored, byHash := storedToken("tok-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), false)
	refreshRepo := &fakeRefreshRepo{byHash: byHash}
	uc := NewLogoutUseCase(refreshRepo, &fakeHasher{})

	if err := uc.Execute(context.Background(), LogoutCmd{RawRefreshToken: "raw-refresh"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !refreshRepo.revokeCalled || refreshRepo.revokedID != stored.ID {
		t.Errorf("expected revoke of %q, got called=%v id=%q", stored.ID, refreshRepo.revokeCalled, refreshRepo.revokedID)
	}
	if refreshRepo.revokedByID != "" {
		t.Errorf("logout should not set a replacement, got %q", refreshRepo.revokedByID)
	}
}

func TestLogout_Idempotent(t *testing.T) {
	t.Run("empty token is a no-op", func(t *testing.T) {
		refreshRepo := &fakeRefreshRepo{}
		uc := NewLogoutUseCase(refreshRepo, &fakeHasher{})
		if err := uc.Execute(context.Background(), LogoutCmd{RawRefreshToken: ""}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if refreshRepo.revokeCalled {
			t.Error("revoke should not be called for empty token")
		}
	})

	t.Run("unknown token is a no-op", func(t *testing.T) {
		refreshRepo := &fakeRefreshRepo{} // sin preload → FindByHash falla
		uc := NewLogoutUseCase(refreshRepo, &fakeHasher{})
		if err := uc.Execute(context.Background(), LogoutCmd{RawRefreshToken: "ghost"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if refreshRepo.revokeCalled {
			t.Error("revoke should not be called for unknown token")
		}
	})

	t.Run("already revoked token is a no-op", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		user := newActiveUser(t, tenant.ID(), "user@example.com")
		_, byHash := storedToken("tok-id", "raw-refresh", user, tenant, refreshNow.Add(time.Hour), true)
		refreshRepo := &fakeRefreshRepo{byHash: byHash}
		uc := NewLogoutUseCase(refreshRepo, &fakeHasher{})
		if err := uc.Execute(context.Background(), LogoutCmd{RawRefreshToken: "raw-refresh"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if refreshRepo.revokeCalled {
			t.Error("revoke should not be called for already-revoked token")
		}
	})
}
