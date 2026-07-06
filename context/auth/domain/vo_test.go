package domain

import (
	"errors"
	"testing"
)

func TestParseTenantID(t *testing.T) {
	valid := NewTenantID().String()
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid uuid", valid, false},
		{"empty", "", true},
		{"not a uuid", "abc123", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTenantID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, ErrInvalidID) {
					t.Fatalf("expected ErrInvalidID, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tt.input {
				t.Fatalf("got %q, want %q", got, tt.input)
			}
		})
	}
}

func TestParseUserIDAndApiKeyID(t *testing.T) {
	uid := NewUserID().String()
	if _, err := ParseUserID(uid); err != nil {
		t.Fatalf("valid user id rejected: %v", err)
	}
	if _, err := ParseUserID("nope"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}

	kid := NewApiKeyID().String()
	if _, err := ParseApiKeyID(kid); err != nil {
		t.Fatalf("valid api key id rejected: %v", err)
	}
	if _, err := ParseApiKeyID(""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestIDsIsZero(t *testing.T) {
	if !TenantID("").IsZero() {
		t.Error("empty TenantID should be zero")
	}
	if NewTenantID().IsZero() {
		t.Error("generated TenantID should not be zero")
	}
	if !UserID("").IsZero() {
		t.Error("empty UserID should be zero")
	}
	if !ApiKeyID("").IsZero() {
		t.Error("empty ApiKeyID should be zero")
	}
}

func TestNewEmail(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"normalizes case and spaces", "  User@Example.COM ", "user@example.com", false},
		{"valid simple", "a@b.co", "a@b.co", false},
		{"empty", "", "", true},
		{"no at", "userexample.com", "", true},
		{"at start", "@example.com", "", true},
		{"at end", "user@", "", true},
		{"domain without dot", "user@localhost", "", true},
		{"domain starts with dot", "user@.com", "", true},
		{"domain ends with dot", "user@example.", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewEmail(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("expected ErrInvalidEmail, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	for _, r := range []Role{RoleAdmin, RoleOperator, RoleAccountant, RoleReadOnly, RolePlatformSupport} {
		got, err := ParseRole(r.String())
		if err != nil {
			t.Fatalf("valid role %q rejected: %v", r, err)
		}
		if got != r {
			t.Fatalf("got %q, want %q", got, r)
		}
	}
	if _, err := ParseRole("superuser"); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestParseEnvironment(t *testing.T) {
	test, err := ParseEnvironment("test")
	if err != nil || !test.IsTest() || test.IsProd() {
		t.Fatalf("test environment wrong: env=%v err=%v", test, err)
	}
	prod, err := ParseEnvironment("production")
	if err != nil || !prod.IsProd() || prod.IsTest() {
		t.Fatalf("production environment wrong: env=%v err=%v", prod, err)
	}
	if _, err := ParseEnvironment("staging"); !errors.Is(err, ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment, got %v", err)
	}
}

func TestParseScopeAndAllScopes(t *testing.T) {
	all := AllScopes()
	if len(all) != 4 {
		t.Fatalf("expected 4 scopes, got %d", len(all))
	}
	for _, s := range all {
		got, err := ParseScope(s.String())
		if err != nil {
			t.Fatalf("valid scope %q rejected: %v", s, err)
		}
		if got != s {
			t.Fatalf("got %q, want %q", got, s)
		}
	}
	if _, err := ParseScope("admin:all"); err == nil {
		t.Fatal("expected error for invalid scope")
	}
}
