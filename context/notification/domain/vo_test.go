package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewNotificationID(t *testing.T) {
	if _, err := uuid.Parse(NewNotificationID().String()); err != nil {
		t.Errorf("NewNotificationID not a uuid: %v", err)
	}
	if NewNotificationID() == NewNotificationID() {
		t.Error("NewNotificationID should be unique")
	}
}

func TestParseTenantID(t *testing.T) {
	u := uuid.NewString()
	tid, err := ParseTenantID(u)
	if err != nil || tid.String() != u {
		t.Fatalf("valid parse failed: %q, %v", tid, err)
	}

	t.Run("invalid rejected", func(t *testing.T) {
		if _, err := ParseTenantID("nope"); err == nil {
			t.Error("expected error for invalid tenant id")
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		if _, err := ParseTenantID(""); err == nil {
			t.Error("expected error for empty tenant id")
		}
	})
}

func TestParseChannel(t *testing.T) {
	got, err := ParseChannel("email")
	if err != nil || got != ChannelEmail {
		t.Fatalf("ParseChannel(email) = %q, %v", got, err)
	}
	if got.String() != "email" {
		t.Errorf("String() = %q", got.String())
	}

	t.Run("invalid rejected", func(t *testing.T) {
		_, err := ParseChannel("sms")
		if !errors.Is(err, ErrInvalidChannel) {
			t.Fatalf("expected ErrInvalidChannel, got %v", err)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := ParseChannel("")
		if !errors.Is(err, ErrInvalidChannel) {
			t.Fatalf("expected ErrInvalidChannel, got %v", err)
		}
	})
}

func TestNotificationStatus_String(t *testing.T) {
	cases := map[NotificationStatus]string{
		StatusPending: "pending",
		StatusSent:    "sent",
		StatusFailed:  "failed",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("%v.String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, ok := range []NotificationStatus{StatusPending, StatusSent, StatusFailed} {
		if got, err := ParseStatus(ok.String()); err != nil || got != ok {
			t.Errorf("ParseStatus(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "SENT", "delivered"} {
		if _, err := ParseStatus(bad); !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("ParseStatus(%q): expected ErrInvalidStatus, got %v", bad, err)
		}
	}
}
