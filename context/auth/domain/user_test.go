package domain

import (
	"errors"
	"testing"
	"time"
)

func newTestUser(t *testing.T) *User {
	t.Helper()
	email, err := NewEmail("user@example.com")
	if err != nil {
		t.Fatalf("bad test email: %v", err)
	}
	u, err := NewUser(NewUserID(), NewTenantID(), email, "hashed-pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return u
}

func TestNewUser(t *testing.T) {
	t.Run("empty password hash is rejected", func(t *testing.T) {
		email, _ := NewEmail("user@example.com")
		if _, err := NewUser(NewUserID(), NewTenantID(), email, ""); !errors.Is(err, ErrEmptyPassword) {
			t.Fatalf("expected ErrEmptyPassword, got %v", err)
		}
	})

	t.Run("starts active and emits UserRegistered", func(t *testing.T) {
		id := NewUserID()
		tid := NewTenantID()
		email, _ := NewEmail("user@example.com")
		u, err := NewUser(id, tid, email, "hashed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Status() != UserStatusActive {
			t.Errorf("status = %q, want active", u.Status())
		}
		events := u.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		reg, ok := events[0].(UserRegisteredEvent)
		if !ok {
			t.Fatalf("expected UserRegisteredEvent, got %T", events[0])
		}
		if reg.UserID != id.String() || reg.TenantID != tid.String() || reg.Email != "user@example.com" {
			t.Errorf("event payload mismatch: %+v", reg)
		}
	})
}

func TestReconstituteUser(t *testing.T) {
	id := NewUserID()
	tid := NewTenantID()
	email, _ := NewEmail("user@example.com")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	u := ReconstituteUser(id, tid, email, "hash", UserStatusSuspended, created, updated)

	if u.ID() != id || u.TenantID() != tid || u.Email() != email {
		t.Error("identity fields not restored")
	}
	if u.PasswordHash() != "hash" || u.Status() != UserStatusSuspended {
		t.Error("hash/status not restored")
	}
	if !u.CreatedAt().Equal(created) || !u.UpdatedAt().Equal(updated) {
		t.Error("timestamps not restored")
	}
	if len(u.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func TestUserCanAuthenticate(t *testing.T) {
	u := newTestUser(t)
	if err := u.CanAuthenticate(); err != nil {
		t.Fatalf("active user should authenticate: %v", err)
	}
	_ = u.Suspend()
	if err := u.CanAuthenticate(); !errors.Is(err, ErrUserSuspended) {
		t.Fatalf("expected ErrUserSuspended, got %v", err)
	}
}

func TestUserUpdatePasswordHash(t *testing.T) {
	u := newTestUser(t)
	if err := u.UpdatePasswordHash(""); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got %v", err)
	}
	if err := u.UpdatePasswordHash("new-hash"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.PasswordHash() != "new-hash" {
		t.Errorf("hash = %q, want new-hash", u.PasswordHash())
	}
}

func TestUserSuspend(t *testing.T) {
	u := newTestUser(t)
	u.PullEvents() // descartar el registered

	if err := u.Suspend(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Status() != UserStatusSuspended {
		t.Errorf("status = %q, want suspended", u.Status())
	}
	events := u.PullEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if _, ok := events[0].(UserSuspendedEvent); !ok {
		t.Fatalf("expected UserSuspendedEvent, got %T", events[0])
	}

	if err := u.Suspend(); err == nil {
		t.Fatal("expected error suspending an already suspended user")
	}
}
