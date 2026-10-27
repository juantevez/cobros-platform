package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func validRegisterCmd() RegisterTenantCmd {
	return RegisterTenantCmd{
		LegalName: "Acme SA",
		Email:     "admin@acme.com",
		Password:  "Secure123!",
	}
}

func TestRegisterTenant_Success(t *testing.T) {
	repo := newFakeTenantRepo()
	users := newFakeUserRepo()
	members := newFakeMembershipRepo()
	pub := &fakePublisher{}
	uc := NewRegisterTenantUseCase(repo, users, members, &fakeHasher{}, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), validRegisterCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TenantID == "" {
		t.Fatal("expected a tenant id")
	}
	if res.UserID == "" {
		t.Fatal("expected an admin user id")
	}
	// El tenant persistido nace pending + test.
	id, _ := domain.ParseTenantID(res.TenantID)
	saved := repo.tenants[id]
	if saved == nil {
		t.Fatal("tenant was not saved")
	}
	if saved.Status() != domain.TenantStatusPending || !saved.Environment().IsTest() {
		t.Errorf("unexpected initial state: status=%s env=%s", saved.Status(), saved.Environment())
	}
	// El admin inicial se crea con rol admin en el mismo tenant.
	uid, _ := domain.ParseUserID(res.UserID)
	admin := users.byID[uid]
	if admin == nil {
		t.Fatal("admin user was not saved")
	}
	if admin.Email().String() != "admin@acme.com" {
		t.Errorf("unexpected admin email: %s", admin.Email())
	}
	m, ok := members.items[string(uid)+"|"+string(id)]
	if !ok {
		t.Fatal("admin membership was not saved")
	}
	if m.Role() != domain.RoleAdmin {
		t.Errorf("expected role admin, got %s", m.Role())
	}
	// TenantCreatedEvent (+ UserRegistered si el agregado lo emite).
	var foundCreated bool
	for _, ev := range pub.published {
		if _, ok := ev.(domain.TenantCreatedEvent); ok {
			foundCreated = true
		}
	}
	if !foundCreated {
		t.Fatal("expected TenantCreatedEvent to be published")
	}
}

func TestRegisterTenant_EmptyLegalName(t *testing.T) {
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	cmd := validRegisterCmd()
	cmd.LegalName = ""
	_, err := uc.Execute(context.Background(), cmd)
	if !errors.Is(err, domain.ErrEmptyLegalName) {
		t.Fatalf("expected ErrEmptyLegalName, got %v", err)
	}
}

func TestRegisterTenant_InvalidEmail(t *testing.T) {
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	cmd := validRegisterCmd()
	cmd.Email = "no-es-un-email"
	_, err := uc.Execute(context.Background(), cmd)
	if !errors.Is(err, domain.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestRegisterTenant_EmptyPassword(t *testing.T) {
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})
	cmd := validRegisterCmd()
	cmd.Password = ""
	_, err := uc.Execute(context.Background(), cmd)
	if !errors.Is(err, domain.ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got %v", err)
	}
}

func TestRegisterTenant_HasherErrorPropagates(t *testing.T) {
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{hashErr: errBoom}, fakeTx{}, &fakePublisher{})
	_, err := uc.Execute(context.Background(), validRegisterCmd())
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestRegisterTenant_SaveErrorPropagates(t *testing.T) {
	repo := newFakeTenantRepo()
	repo.saveErr = errBoom
	uc := NewRegisterTenantUseCase(repo, newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), validRegisterCmd())
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestRegisterTenant_PublisherErrorPropagates(t *testing.T) {
	pub := &fakePublisher{err: errBoom}
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), newFakeUserRepo(), newFakeMembershipRepo(), &fakeHasher{}, fakeTx{}, pub)

	_, err := uc.Execute(context.Background(), validRegisterCmd())
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
