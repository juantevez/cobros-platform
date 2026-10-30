package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func newAuthUC(
	tenant *domain.Tenant,
	user *domain.User,
	memberships []domain.Membership,
	hasher *fakeHasher,
	issuer *fakeTokenIssuer,
	refreshRepo *fakeRefreshRepo,
) *AuthenticateUseCase {
	tenantRepo := newFakeTenantRepo()
	if tenant != nil {
		tenantRepo.tenants[tenant.ID()] = tenant
	}
	userRepo := newFakeUserRepo()
	if user != nil {
		userRepo.byID[user.ID()] = user
		userRepo.byEmail[user.TenantID().String()+"|"+user.Email().String()] = user
	}
	return NewAuthenticateUseCase(
		tenantRepo, userRepo, newFakeMembershipRepo(memberships...),
		refreshRepo, hasher, issuer, fakeClock{now: time.Unix(1_700_000_000, 0).UTC()},
		fakeTx{}, &fakePublisher{},
	)
}

func TestAuthenticate_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())

	hasher := &fakeHasher{verifyResult: true}
	issuer := &fakeTokenIssuer{access: "access-jwt", refresh: "raw-refresh"}
	refreshRepo := &fakeRefreshRepo{}
	uc := newAuthUC(tenant, user, []domain.Membership{membership}, hasher, issuer, refreshRepo)

	pair, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.AccessToken != "access-jwt" || pair.RefreshToken != "raw-refresh" {
		t.Errorf("unexpected token pair: %+v", pair)
	}
	if pair.ExpiresIn != accessTokenSeconds {
		t.Errorf("expiresIn = %d, want %d", pair.ExpiresIn, accessTokenSeconds)
	}
	// El claim debe llevar el rol y ambiente correctos.
	if issuer.gotClaims.Role != domain.RoleOperator || !issuer.gotClaims.Environment.IsProd() {
		t.Errorf("claims mismatch: %+v", issuer.gotClaims)
	}
	// El refresh token se persiste hasheado, nunca en claro.
	if refreshRepo.saved == nil {
		t.Fatal("refresh token not persisted")
	}
	if refreshRepo.saved.TokenHash != hashRefreshToken("raw-refresh") {
		t.Errorf("stored hash = %q, want hashRefreshToken(raw-refresh)", refreshRepo.saved.TokenHash)
	}
	if refreshRepo.saved.TokenHash == "raw-refresh" {
		t.Error("refresh token must not be stored in plaintext")
	}
}

func TestAuthenticate_TenantSuspended(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	_ = tenant.Suspend("fraud")
	tenant.PullEvents()
	user := newActiveUser(t, tenant.ID(), "user@example.com")

	uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, domain.ErrTenantSuspended) {
		t.Fatalf("expected ErrTenantSuspended, got %v", err)
	}
}

func TestAuthenticate_TenantNotFoundIsInvalidCredentials(t *testing.T) {
	uc := newAuthUC(nil, nil, nil, &fakeHasher{}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: domain.NewTenantID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticate_InvalidEmailIsInvalidCredentials(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	uc := newAuthUC(tenant, nil, nil, &fakeHasher{}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "not-an-email", Password: "secret",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticate_UnknownUserIsInvalidCredentials(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	uc := newAuthUC(tenant, nil, nil, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "ghost@example.com", Password: "secret",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticate_SuspendedUser(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	_ = user.Suspend()
	user.PullEvents()

	uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	})
	if !errors.Is(err, domain.ErrUserSuspended) {
		t.Fatalf("expected ErrUserSuspended, got %v", err)
	}
}

func TestAuthenticate_WrongPassword(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")

	uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: false}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
	_, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "wrong",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticate_PublishesLoginEvent(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())
	users := newFakeUserRepo()
	users.byID[user.ID()] = user
	users.byEmail[user.TenantID().String()+"|"+user.Email().String()] = user
	pub := &fakePublisher{}
	uc := NewAuthenticateUseCase(newFakeTenantRepo(tenant), users, newFakeMembershipRepo(membership),
		&fakeRefreshRepo{}, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "a", refresh: "r"},
		fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}, fakeTx{}, pub)

	if _, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	ev, ok := pub.published[0].(domain.UserLoggedInEvent)
	if !ok || ev.EventType() != "auth.user.login.v1" || ev.UserID != user.ID().String() || ev.TenantID != tenant.ID().String() {
		t.Errorf("unexpected event: %+v", pub.published[0])
	}
}

func TestAuthenticate_PublishErrorFailsLogin(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	user := newActiveUser(t, tenant.ID(), "user@example.com")
	membership := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.NewUserID())
	users := newFakeUserRepo()
	users.byEmail[user.TenantID().String()+"|"+user.Email().String()] = user
	uc := NewAuthenticateUseCase(newFakeTenantRepo(tenant), users, newFakeMembershipRepo(membership),
		&fakeRefreshRepo{}, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{access: "a", refresh: "r"},
		fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}, fakeTx{}, &fakePublisher{err: errors.New("outbox down")})

	if _, err := uc.Execute(context.Background(), AuthenticateCmd{
		TenantID: tenant.ID().String(), Email: "user@example.com", Password: "secret",
	}); err == nil {
		t.Fatal("login must fail if the audit event cannot be recorded (same tx as the refresh token)")
	}
}
