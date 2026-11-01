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

// Anti-enumeración: sin la contraseña no se distingue un tenant suspendido,
// un usuario suspendido ni uno sin rol de un login fallido cualquiera.
func TestAuthenticate_StatusOnlyRevealedWithValidPassword(t *testing.T) {
	cmd := func(tenant *domain.Tenant) AuthenticateCmd {
		return AuthenticateCmd{TenantID: tenant.ID().String(), Email: "user@example.com", Password: "wrong"}
	}

	t.Run("suspended tenant + wrong password", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		_ = tenant.Suspend("fraud")
		user := newActiveUser(t, tenant.ID(), "user@example.com")
		uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: false}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
		if _, err := uc.Execute(context.Background(), cmd(tenant)); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("suspended user + wrong password", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		user := newActiveUser(t, tenant.ID(), "user@example.com")
		_ = user.Suspend()
		uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: false}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
		if _, err := uc.Execute(context.Background(), cmd(tenant)); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("no membership + right password", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		user := newActiveUser(t, tenant.ID(), "user@example.com")
		uc := newAuthUC(tenant, user, nil, &fakeHasher{verifyResult: true}, &fakeTokenIssuer{}, &fakeRefreshRepo{})
		if _, err := uc.Execute(context.Background(), cmd(tenant)); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials (was 404 membership not found)", err)
		}
	})
}

// Un email inexistente también paga la verificación (contra el hash señuelo):
// antes respondía más rápido y delataba qué emails existen.
func TestAuthenticate_UnknownUserStillVerifiesPassword(t *testing.T) {
	for name, email := range map[string]string{"unknown email": "ghost@example.com", "malformed email": "nope"} {
		t.Run(name, func(t *testing.T) {
			tenant := newActiveTenant(t, domain.EnvironmentProduction)
			hasher := &fakeHasher{verifyResult: true} // ni un señuelo que "coincida" deja entrar
			uc := newAuthUC(tenant, nil, nil, hasher, &fakeTokenIssuer{}, &fakeRefreshRepo{})
			_, err := uc.Execute(context.Background(), AuthenticateCmd{TenantID: tenant.ID().String(), Email: email, Password: "x"})
			if !errors.Is(err, domain.ErrInvalidCredentials) || hasher.verifyCalls != 1 {
				t.Fatalf("err=%v verifyCalls=%d, want ErrInvalidCredentials and 1 verification", err, hasher.verifyCalls)
			}
		})
	}

	t.Run("unknown tenant", func(t *testing.T) {
		hasher := &fakeHasher{}
		uc := newAuthUC(nil, nil, nil, hasher, &fakeTokenIssuer{}, &fakeRefreshRepo{})
		_, _ = uc.Execute(context.Background(), AuthenticateCmd{TenantID: domain.NewTenantID().String(), Email: "a@b.com", Password: "x"})
		if hasher.verifyCalls != 1 {
			t.Errorf("verifyCalls = %d, want 1", hasher.verifyCalls)
		}
	})
}
