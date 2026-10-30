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

	t.Run("accepts any well-formed context.fact", func(t *testing.T) {
		for _, ok := range []string{"something.custom", "compliance.alert.review_proposed", "audit.unmapped_event", "a1.b2"} {
			if _, err := ParseAction(ok); err != nil {
				t.Errorf("%q rejected: %v", ok, err)
			}
		}
	})

	t.Run("malformed rejected", func(t *testing.T) {
		for _, bad := range []string{"", "single", "Auth.Tenant", "auth..created", ".auth", "auth.", "auth.tenant created", "auth.tenant.created.v1 "} {
			if _, err := ParseAction(bad); !errors.Is(err, ErrInvalidAction) {
				t.Errorf("%q: expected ErrInvalidAction, got %v", bad, err)
			}
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
