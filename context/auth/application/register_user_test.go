package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRegisterUser_Success(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	userRepo := newFakeUserRepo()
	memberRepo := newFakeMembershipRepo()
	hasher := &fakeHasher{}
	pub := &fakePublisher{}
	uc := NewRegisterUserUseCase(newFakeTenantRepo(tenant), userRepo, memberRepo, hasher, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), RegisterUserCmd{
		TenantID: tenant.ID().String(),
		Email:    "New.User@Example.com",
		Password: "s3cret",
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.UserID == "" {
		t.Fatal("expected a user id")
	}
	// El usuario se guarda con el email normalizado y el hash del password.
	uid, _ := domain.ParseUserID(res.UserID)
	saved := userRepo.byID[uid]
	if saved == nil {
		t.Fatal("user not saved")
	}
	if saved.Email().String() != "new.user@example.com" {
		t.Errorf("email = %q, want normalized", saved.Email())
	}
	if saved.PasswordHash() != "hash:s3cret" {
		t.Errorf("password hash = %q, want hash:s3cret", saved.PasswordHash())
	}
	// La membership se crea con el rol pedido.
	if memberRepo.saved == nil || memberRepo.saved.Role() != domain.RoleAdmin {
		t.Fatal("membership not created with admin role")
	}
	// Se publica UserRegisteredEvent.
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.UserRegisteredEvent); !ok {
		t.Fatalf("expected UserRegisteredEvent, got %T", pub.published[0])
	}
}

func TestRegisterUser_ValidationErrors(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	newUC := func() *RegisterUserUseCase {
		return NewRegisterUserUseCase(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	}
	valid := RegisterUserCmd{TenantID: tenant.ID().String(), Email: "u@example.com", Password: "pw", Role: "operator"}

	t.Run("invalid tenant id", func(t *testing.T) {
		cmd := valid
		cmd.TenantID = "nope"
		if _, err := newUC().Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidID) {
			t.Fatalf("expected ErrInvalidID, got %v", err)
		}
	})
	t.Run("invalid email", func(t *testing.T) {
		cmd := valid
		cmd.Email = "bad"
		if _, err := newUC().Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidEmail) {
			t.Fatalf("expected ErrInvalidEmail, got %v", err)
		}
	})
	t.Run("empty password", func(t *testing.T) {
		cmd := valid
		cmd.Password = ""
		if _, err := newUC().Execute(context.Background(), cmd); !errors.Is(err, domain.ErrEmptyPassword) {
			t.Fatalf("expected ErrEmptyPassword, got %v", err)
		}
	})
	t.Run("invalid role", func(t *testing.T) {
		cmd := valid
		cmd.Role = "wizard"
		if _, err := newUC().Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidRole) {
			t.Fatalf("expected ErrInvalidRole, got %v", err)
		}
	})
}

func TestRegisterUser_TenantSuspended(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	_ = tenant.Suspend("fraud")
	tenant.PullEvents()

	uc := NewRegisterUserUseCase(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	_, err := uc.Execute(context.Background(), RegisterUserCmd{
		TenantID: tenant.ID().String(), Email: "u@example.com", Password: "pw", Role: "operator",
	})
	if !errors.Is(err, domain.ErrTenantSuspended) {
		t.Fatalf("expected ErrTenantSuspended, got %v", err)
	}
}

func TestRegisterUser_HashErrorPropagates(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	hasher := &fakeHasher{hashErr: errBoom}
	uc := NewRegisterUserUseCase(newFakeTenantRepo(tenant), newFakeUserRepo(), newFakeMembershipRepo(), hasher, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), RegisterUserCmd{
		TenantID: tenant.ID().String(), Email: "u@example.com", Password: "pw", Role: "operator",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestRegisterUser_DuplicateEmail(t *testing.T) {
	tenant := newActiveTenant(t, domain.EnvironmentProduction)
	userRepo := newFakeUserRepo()
	userRepo.saveErr = domain.ErrEmailAlreadyExists
	uc := NewRegisterUserUseCase(newFakeTenantRepo(tenant), userRepo, newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), RegisterUserCmd{
		TenantID: tenant.ID().String(), Email: "u@example.com", Password: "pw", Role: "operator",
	})
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}
