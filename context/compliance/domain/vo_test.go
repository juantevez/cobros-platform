package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewAlertID(t *testing.T) {
	id := NewAlertID()
	if _, err := uuid.Parse(id.String()); err != nil {
		t.Errorf("NewAlertID produced non-uuid %q: %v", id, err)
	}
	a, b := NewAlertID(), NewAlertID()
	if a == b {
		t.Error("NewAlertID should produce unique values")
	}
}

func TestParseTenantID(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		u := uuid.NewString()
		tid, err := ParseTenantID(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tid.String() != u {
			t.Errorf("got %q, want %q", tid, u)
		}
	})

	t.Run("invalid rejected", func(t *testing.T) {
		if _, err := ParseTenantID("not-a-uuid"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		if _, err := ParseTenantID(""); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseAlertID(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		u := uuid.NewString()
		id, err := ParseAlertID(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id.String() != u {
			t.Errorf("got %q, want %q", id, u)
		}
	})

	t.Run("invalid rejected", func(t *testing.T) {
		if _, err := ParseAlertID("bogus"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTypeStrings(t *testing.T) {
	if AlertSanctionsMatch.String() != "sanctions_match" {
		t.Errorf("AlertType.String() = %q", AlertSanctionsMatch.String())
	}
	if RiskHigh.String() != "high" {
		t.Errorf("RiskLevel.String() = %q", RiskHigh.String())
	}
	if StatusOpen.String() != "open" {
		t.Errorf("AlertStatus.String() = %q", StatusOpen.String())
	}
}

func TestParseDisposition(t *testing.T) {
	t.Run("cleared", func(t *testing.T) {
		s, err := ParseDisposition("cleared")
		if err != nil || s != StatusCleared {
			t.Fatalf("got %q, %v", s, err)
		}
	})

	t.Run("confirmed", func(t *testing.T) {
		s, err := ParseDisposition("confirmed")
		if err != nil || s != StatusConfirmed {
			t.Fatalf("got %q, %v", s, err)
		}
	})

	t.Run("open is not a valid disposition", func(t *testing.T) {
		_, err := ParseDisposition("open")
		if !errors.Is(err, ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		_, err := ParseDisposition("whatever")
		if !errors.Is(err, ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := ParseDisposition("")
		if !errors.Is(err, ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
	})
}
