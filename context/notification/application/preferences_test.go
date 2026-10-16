package application

import (
	"context"
	"testing"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

func TestUpdatePreferenceUseCase_Execute(t *testing.T) {
	baseCmd := func() UpdatePreferenceCmd {
		return UpdatePreferenceCmd{
			TenantID:       validTenant(),
			EventType:      "payment.captured",
			Enabled:        false,
			RecipientEmail: "ops@x.com",
		}
	}

	t.Run("upserts preference", func(t *testing.T) {
		pr := newPrefRepo()
		uc := NewUpdatePreferenceUseCase(pr)

		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pr.upserted) != 1 {
			t.Fatalf("expected 1 upsert, got %d", len(pr.upserted))
		}
		p := pr.upserted[0]
		if p.EventType() != "payment.captured" || p.Enabled() != false {
			t.Errorf("preference fields wrong: %+v", p)
		}
		if p.RecipientEmail() != "ops@x.com" || p.Channel() != domain.ChannelEmail {
			t.Errorf("preference fields wrong: %+v", p)
		}
	})

	t.Run("rejects invalid tenant id", func(t *testing.T) {
		uc := NewUpdatePreferenceUseCase(newPrefRepo())
		cmd := baseCmd()
		cmd.TenantID = "bad"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})

	t.Run("rejects event type without template", func(t *testing.T) {
		pr := newPrefRepo()
		uc := NewUpdatePreferenceUseCase(pr)
		cmd := baseCmd()
		cmd.EventType = "unknown.event"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for event type without template")
		}
		if len(pr.upserted) != 0 {
			t.Error("should not upsert when template missing")
		}
	})

	t.Run("propagates upsert error", func(t *testing.T) {
		pr := newPrefRepo()
		pr.upsertErr = errBoom
		uc := NewUpdatePreferenceUseCase(pr)
		if err := uc.Execute(context.Background(), baseCmd()); err == nil {
			t.Fatal("expected error from repo")
		}
	})
}

func TestGetPreferencesUseCase_Execute(t *testing.T) {
	t.Run("returns default enabled for event types without saved preference", func(t *testing.T) {
		pr := newPrefRepo()
		uc := NewGetPreferencesUseCase(pr)

		views, err := uc.Execute(context.Background(), GetPreferencesQuery{TenantID: validTenant()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != len(domain.Templates) {
			t.Fatalf("expected %d views, got %d", len(domain.Templates), len(views))
		}
		for _, v := range views {
			if !v.Enabled {
				t.Errorf("default for %q should be enabled", v.EventType)
			}
			if v.Channel != domain.ChannelEmail.String() {
				t.Errorf("channel = %q", v.Channel)
			}
		}
	})

	t.Run("saved preference overrides default", func(t *testing.T) {
		pr := newPrefRepo()
		pr.listed = []*domain.NotificationPreference{
			domain.NewNotificationPreference(domain.TenantID("t"), "payment.captured",
				domain.ChannelEmail, false, "custom@x.com"),
		}
		uc := NewGetPreferencesUseCase(pr)

		views, err := uc.Execute(context.Background(), GetPreferencesQuery{TenantID: validTenant()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var found bool
		for _, v := range views {
			if v.EventType == "payment.captured" {
				found = true
				if v.Enabled {
					t.Error("saved preference should be disabled")
				}
				if v.RecipientEmail != "custom@x.com" {
					t.Errorf("recipient = %q", v.RecipientEmail)
				}
			}
		}
		if !found {
			t.Error("payment.captured view not present")
		}
	})

	t.Run("rejects invalid tenant id", func(t *testing.T) {
		uc := NewGetPreferencesUseCase(newPrefRepo())
		if _, err := uc.Execute(context.Background(), GetPreferencesQuery{TenantID: "bad"}); err == nil {
			t.Fatal("expected error for invalid tenant id")
		}
	})

	t.Run("propagates list error", func(t *testing.T) {
		pr := newPrefRepo()
		pr.listErr = errBoom
		uc := NewGetPreferencesUseCase(pr)
		if _, err := uc.Execute(context.Background(), GetPreferencesQuery{TenantID: validTenant()}); err == nil {
			t.Fatal("expected error from repo")
		}
	})
}
