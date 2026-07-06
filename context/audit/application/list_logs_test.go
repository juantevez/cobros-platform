package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

func TestListLogsUseCase_Execute(t *testing.T) {
	t.Run("no tenant lists recent", func(t *testing.T) {
		e := buildEntry("t1", domain.ActionLogin, domain.ResourceUser, "u1", nil)
		repo := &fakeRepo{recent: []*domain.AuditLogEntry{e}}
		uc := NewListLogsUseCase(repo)

		views, err := uc.Execute(context.Background(), ListLogsQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 1 {
			t.Fatalf("expected 1 view, got %d", len(views))
		}
		v := views[0]
		if v.Action != "auth.user.login" || v.ResourceType != "user" || v.Hash != e.HashHex() {
			t.Errorf("view mapping mismatch: %+v", v)
		}
		if v.CreatedAt == "" {
			t.Error("CreatedAt should be formatted")
		}
	})

	t.Run("tenant filter routes to ListByTenant", func(t *testing.T) {
		e := buildEntry("tenant-9", domain.ActionLogin, domain.ResourceUser, "u1", nil)
		repo := &fakeRepo{byTenant: []*domain.AuditLogEntry{e}}
		uc := NewListLogsUseCase(repo)

		views, err := uc.Execute(context.Background(), ListLogsQuery{TenantID: "tenant-9"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastTenantID != "tenant-9" {
			t.Errorf("ListByTenant called with %q", repo.lastTenantID)
		}
		if len(views) != 1 || views[0].TenantID != "tenant-9" {
			t.Errorf("unexpected views: %+v", views)
		}
	})

	t.Run("default limit applied", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewListLogsUseCase(repo)
		if _, err := uc.Execute(context.Background(), ListLogsQuery{Limit: 0}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != defaultLogLimit {
			t.Errorf("limit = %d, want %d", repo.lastLimit, defaultLogLimit)
		}
	})

	t.Run("limit capped at max", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewListLogsUseCase(repo)
		if _, err := uc.Execute(context.Background(), ListLogsQuery{Limit: 9999}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != maxLogLimit {
			t.Errorf("limit = %d, want %d", repo.lastLimit, maxLogLimit)
		}
	})

	t.Run("explicit limit within range preserved", func(t *testing.T) {
		repo := &fakeRepo{}
		uc := NewListLogsUseCase(repo)
		if _, err := uc.Execute(context.Background(), ListLogsQuery{Limit: 25}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != 25 {
			t.Errorf("limit = %d, want 25", repo.lastLimit)
		}
	})

	t.Run("empty result yields empty slice", func(t *testing.T) {
		repo := &fakeRepo{recent: nil}
		uc := NewListLogsUseCase(repo)
		views, err := uc.Execute(context.Background(), ListLogsQuery{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 0 {
			t.Errorf("expected empty, got %d", len(views))
		}
	})

	t.Run("recent error propagated", func(t *testing.T) {
		repo := &fakeRepo{recentErr: errBoom}
		uc := NewListLogsUseCase(repo)
		if _, err := uc.Execute(context.Background(), ListLogsQuery{}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("tenant error propagated", func(t *testing.T) {
		repo := &fakeRepo{tenantErr: errBoom}
		uc := NewListLogsUseCase(repo)
		if _, err := uc.Execute(context.Background(), ListLogsQuery{TenantID: "t"}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
