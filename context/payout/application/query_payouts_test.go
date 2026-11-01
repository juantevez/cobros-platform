package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

func TestGetPayoutUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("returns view of tenant's payout", func(t *testing.T) {
		at := time.Date(2026, 10, 1, 15, 30, 0, 0, time.UTC)
		p := buildConfirmedPayout(tenantA, "REF-9", at)
		repo := &fakeRepo{byID: map[domain.PayoutID]*domain.Payout{p.ID(): p}}

		v, err := NewGetPayoutUseCase(repo).Execute(ctx, GetPayoutQuery{TenantID: tenantA, PayoutID: p.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ba := validBankAccount()
		if v.ID != p.ID().String() || v.TenantID != tenantA {
			t.Errorf("ids not mapped: %+v", v)
		}
		if v.Amount != 5000 || v.Currency != "ARS" || v.Status != "confirmed" {
			t.Errorf("amount/status not mapped: %+v", v)
		}
		if v.BankAccountType != ba.AccountType || v.BankAccountNum != ba.MaskedAccountNumber() || v.HolderName != ba.HolderName {
			t.Errorf("bank account not mapped: %+v", v)
		}
		if v.BankReference != "REF-9" || v.FailureReason != "" {
			t.Errorf("reference/reason not mapped: %+v", v)
		}
		if v.InitiatedAt == nil || !v.InitiatedAt.Equal(at) || v.ConfirmedAt == nil || v.FailedAt != nil {
			t.Errorf("timestamps not mapped: %+v", v)
		}
		if v.CreatedAt != "2026-10-01T15:30:00Z" {
			t.Errorf("created_at = %q, want RFC3339", v.CreatedAt)
		}
	})

	t.Run("payout of another tenant is reported as not found", func(t *testing.T) {
		p := buildPayout(tenantB, 100)
		repo := &fakeRepo{byID: map[domain.PayoutID]*domain.Payout{p.ID(): p}}

		_, err := NewGetPayoutUseCase(repo).Execute(ctx, GetPayoutQuery{TenantID: tenantA, PayoutID: p.ID().String()})
		if !errors.Is(err, domain.ErrPayoutNotFound) {
			t.Fatalf("expected ErrPayoutNotFound, got %v", err)
		}
	})

	t.Run("repo error is propagated", func(t *testing.T) {
		repo := &fakeRepo{findErr: errBoom}

		_, err := NewGetPayoutUseCase(repo).Execute(ctx, GetPayoutQuery{TenantID: tenantA, PayoutID: domain.NewPayoutID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("missing payout returns ErrPayoutNotFound", func(t *testing.T) {
		repo := &fakeRepo{}

		_, err := NewGetPayoutUseCase(repo).Execute(ctx, GetPayoutQuery{TenantID: tenantA, PayoutID: domain.NewPayoutID().String()})
		if !errors.Is(err, domain.ErrPayoutNotFound) {
			t.Fatalf("expected ErrPayoutNotFound, got %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := NewGetPayoutUseCase(&fakeRepo{}).Execute(ctx, GetPayoutQuery{TenantID: "x", PayoutID: domain.NewPayoutID().String()})
		if !errors.Is(err, domain.ErrInvalidID) || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
	})

	t.Run("invalid payout id", func(t *testing.T) {
		_, err := NewGetPayoutUseCase(&fakeRepo{}).Execute(ctx, GetPayoutQuery{TenantID: tenantA, PayoutID: "x"})
		if !errors.Is(err, domain.ErrInvalidID) || !strings.Contains(err.Error(), "invalid payout id") {
			t.Fatalf("expected invalid payout id error, got %v", err)
		}
	})
}

func TestListPayoutsUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("maps every payout to a view preserving order", func(t *testing.T) {
		p1 := buildPayout(tenantA, 100)
		p2 := buildPayout(tenantA, 200)
		repo := &fakeRepo{list: []*domain.Payout{p1, p2}}

		views, err := NewListPayoutsUseCase(repo).Execute(ctx, ListPayoutsQuery{TenantID: tenantA, Limit: 5})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 2 {
			t.Fatalf("expected 2 views, got %d", len(views))
		}
		if views[0].ID != p1.ID().String() || views[1].ID != p2.ID().String() {
			t.Errorf("order not preserved: %v, %v", views[0].ID, views[1].ID)
		}
		if views[0].Status != "initiated" || views[0].InitiatedAt != nil {
			t.Errorf("initiated payout mapped incorrectly: %+v", views[0])
		}
		if repo.lastTenantID != domain.TenantID(tenantA) || repo.lastLimit != 5 {
			t.Errorf("repo called with tenant=%q limit=%d", repo.lastTenantID, repo.lastLimit)
		}
	})

	t.Run("empty result returns empty non-nil slice", func(t *testing.T) {
		views, err := NewListPayoutsUseCase(&fakeRepo{}).Execute(ctx, ListPayoutsQuery{TenantID: tenantA})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if views == nil || len(views) != 0 {
			t.Errorf("expected empty non-nil slice, got %#v", views)
		}
	})

	for _, limit := range []int{0, -3} {
		t.Run("non-positive limit uses default", func(t *testing.T) {
			repo := &fakeRepo{}
			if _, err := NewListPayoutsUseCase(repo).Execute(ctx, ListPayoutsQuery{TenantID: tenantA, Limit: limit}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.lastLimit != defaultPayoutLimit {
				t.Errorf("limit = %d, want %d", repo.lastLimit, defaultPayoutLimit)
			}
		})
	}

	// Sin tope, ?limit=1000000 traía la tabla entera del tenant.
	t.Run("limit above max is capped", func(t *testing.T) {
		repo := &fakeRepo{}
		if _, err := NewListPayoutsUseCase(repo).Execute(ctx, ListPayoutsQuery{TenantID: tenantA, Limit: 1000000}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.lastLimit != maxPayoutLimit {
			t.Errorf("limit = %d, want %d", repo.lastLimit, maxPayoutLimit)
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		repo := &fakeRepo{listErr: errBoom}

		_, err := NewListPayoutsUseCase(repo).Execute(ctx, ListPayoutsQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "list payouts") {
			t.Errorf("error not wrapped with context: %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		repo := &fakeRepo{}
		_, err := NewListPayoutsUseCase(repo).Execute(ctx, ListPayoutsQuery{TenantID: "nope"})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
		if repo.lastLimit != 0 {
			t.Error("repo should not be called on invalid tenant")
		}
	})
}
