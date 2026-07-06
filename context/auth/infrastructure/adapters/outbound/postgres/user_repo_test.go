package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestUserRepo_SaveAndFind(t *testing.T) {
	pool := requireDB(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	email, _ := domain.NewEmail("roundtrip@example.com")
	user, _ := domain.NewUser(domain.NewUserID(), tenant.ID(), email, "argon2:hash")
	user.PullEvents()

	if err := repo.Save(ctx, user); err != nil {
		t.Fatalf("save: %v", err)
	}

	t.Run("find by id", func(t *testing.T) {
		got, err := repo.FindByID(ctx, user.ID())
		if err != nil {
			t.Fatalf("find by id: %v", err)
		}
		if got.ID() != user.ID() || got.Email().String() != "roundtrip@example.com" {
			t.Errorf("mismatch: %+v", got)
		}
		if got.PasswordHash() != "argon2:hash" || got.Status() != domain.UserStatusActive {
			t.Errorf("fields mismatch: hash=%s status=%s", got.PasswordHash(), got.Status())
		}
		timesClose(t, got.CreatedAt(), user.CreatedAt())
	})

	t.Run("find by email scoped to tenant", func(t *testing.T) {
		got, err := repo.FindByEmail(ctx, tenant.ID(), email)
		if err != nil {
			t.Fatalf("find by email: %v", err)
		}
		if got.ID() != user.ID() {
			t.Errorf("wrong user: %s", got.ID())
		}
		// El mismo email en OTRO tenant no debe encontrarse.
		if _, err := repo.FindByEmail(ctx, domain.NewTenantID(), email); !errors.Is(err, domain.ErrUserNotFound) {
			t.Errorf("email should be scoped by tenant, got %v", err)
		}
	})
}

func TestUserRepo_DuplicateEmail(t *testing.T) {
	pool := requireDB(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	email, _ := domain.NewEmail("dup@example.com")

	u1, _ := domain.NewUser(domain.NewUserID(), tenant.ID(), email, "h1")
	u1.PullEvents()
	if err := repo.Save(ctx, u1); err != nil {
		t.Fatalf("first save: %v", err)
	}

	u2, _ := domain.NewUser(domain.NewUserID(), tenant.ID(), email, "h2")
	u2.PullEvents()
	if err := repo.Save(ctx, u2); !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}

func TestUserRepo_FindByID_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewUserRepository(pool)
	if _, err := repo.FindByID(context.Background(), domain.NewUserID()); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserRepo_Update(t *testing.T) {
	pool := requireDB(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "update@example.com")

	_ = user.UpdatePasswordHash("new-hash")
	_ = user.Suspend()
	if err := repo.Update(ctx, user); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := repo.FindByID(ctx, user.ID())
	if got.PasswordHash() != "new-hash" || got.Status() != domain.UserStatusSuspended {
		t.Errorf("update not persisted: hash=%s status=%s", got.PasswordHash(), got.Status())
	}
}

func TestUserRepo_Update_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewUserRepository(pool)

	email, _ := domain.NewEmail("ghost@example.com")
	user, _ := domain.NewUser(domain.NewUserID(), domain.NewTenantID(), email, "h")
	user.PullEvents()
	if err := repo.Update(context.Background(), user); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}
