package domain

import (
	"errors"
	"testing"
)

func TestParseAction(t *testing.T) {
	t.Run("valid returns action", func(t *testing.T) {
		a, err := ParseAction("auth.user.login")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a != ActionLogin {
			t.Errorf("got %q, want %q", a, ActionLogin)
		}
	})

	t.Run("accepts arbitrary non-empty string", func(t *testing.T) {
		a, err := ParseAction("something.custom")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.String() != "something.custom" {
			t.Errorf("got %q", a.String())
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := ParseAction("")
		if !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("expected ErrInvalidAction, got %v", err)
		}
	})
}

func TestActionString(t *testing.T) {
	if ActionTenantCreated.String() != "auth.tenant.created" {
		t.Errorf("got %q", ActionTenantCreated.String())
	}
}

func TestParseResourceType(t *testing.T) {
	valid := []ResourceType{
		ResourceTenant, ResourceUser, ResourceApiKey, ResourceEntry, ResourceAccount,
	}
	for _, want := range valid {
		t.Run(want.String(), func(t *testing.T) {
			got, err := ParseResourceType(want.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}

	t.Run("unknown rejected", func(t *testing.T) {
		_, err := ParseResourceType("bogus")
		if !errors.Is(err, ErrInvalidResourceType) {
			t.Fatalf("expected ErrInvalidResourceType, got %v", err)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := ParseResourceType("")
		if !errors.Is(err, ErrInvalidResourceType) {
			t.Fatalf("expected ErrInvalidResourceType, got %v", err)
		}
	})
}

func TestResourceTypeString(t *testing.T) {
	if ResourceUser.String() != "user" {
		t.Errorf("got %q", ResourceUser.String())
	}
}
