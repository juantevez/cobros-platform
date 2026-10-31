package application

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// deliveryWithAttempts arma una delivery fallida con un intento registrado.
func deliveryWithAttempts(endpoint *domain.WebhookEndpoint) *domain.WebhookDelivery {
	attemptedAt := time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC)
	next := attemptedAt.Add(30 * time.Second)
	attempt := domain.ReconstituteAttempt(domain.NewAttemptID(), 1, 500, "oops", "", 42, attemptedAt)
	return domain.ReconstituteDelivery(
		domain.NewDeliveryID(), endpoint.ID(), endpoint.TenantID(),
		"payment.captured", "evt-9", []byte(`{}`),
		domain.StatusFailed, 1, &next, []domain.DeliveryAttempt{attempt},
		time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC), attemptedAt,
	)
}

func TestToDeliveryView(t *testing.T) {
	e := newEndpoint(tenantA)
	d := deliveryWithAttempts(e)

	got := toDeliveryView(d)
	want := DeliveryView{
		ID:           d.ID().String(),
		EndpointID:   e.ID().String(),
		EventType:    "payment.captured",
		EventID:      "evt-9",
		Status:       "failed",
		AttemptCount: 1,
		NextRetryAt:  d.NextRetryAt(),
		Attempts: []AttemptView{{
			AttemptNum: 1, HTTPStatus: 500, ResponseBody: "oops", DurationMs: 42,
			AttemptedAt: "2026-10-16T11:00:00Z",
		}},
		CreatedAt: "2026-10-16T10:00:00Z",
		UpdatedAt: "2026-10-16T11:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("view = %+v\nwant %+v", got, want)
	}
}

func TestListDeliveriesUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("maps deliveries of tenant", func(t *testing.T) {
		e := newEndpoint(tenantA)
		repo := newDeliveryRepo()
		repo.list = []*domain.WebhookDelivery{deliveryWithAttempts(e), deliveryWith(e, domain.StatusPending, 0)}

		views, err := NewListDeliveriesUseCase(repo).Execute(ctx, ListDeliveriesQuery{TenantID: tenantA, Limit: 7})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 2 || views[0].Status != "failed" || views[1].Status != "pending" {
			t.Errorf("views = %+v", views)
		}
		if repo.lastTenant != domain.TenantID(tenantA) || repo.lastLimit != 7 {
			t.Errorf("ListByTenant(%q, %d)", repo.lastTenant, repo.lastLimit)
		}
	})

	for _, limit := range []int{0, -1} {
		t.Run("non-positive limit uses default", func(t *testing.T) {
			repo := newDeliveryRepo()
			if _, err := NewListDeliveriesUseCase(repo).Execute(ctx, ListDeliveriesQuery{TenantID: tenantA, Limit: limit}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.lastLimit != defaultDeliveryLimit {
				t.Errorf("limit = %d, want %d", repo.lastLimit, defaultDeliveryLimit)
			}
		})
	}

	for limit, want := range map[int]int{maxDeliveryLimit: maxDeliveryLimit, maxDeliveryLimit + 1: maxDeliveryLimit, 1000000: maxDeliveryLimit} {
		t.Run("limit "+strconv.Itoa(limit)+" is capped", func(t *testing.T) {
			repo := newDeliveryRepo()
			if _, err := NewListDeliveriesUseCase(repo).Execute(ctx, ListDeliveriesQuery{TenantID: tenantA, Limit: limit}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.lastLimit != want {
				t.Errorf("limit = %d, want %d", repo.lastLimit, want)
			}
		})
	}

	t.Run("empty result returns empty non-nil slice", func(t *testing.T) {
		views, err := NewListDeliveriesUseCase(newDeliveryRepo()).Execute(ctx, ListDeliveriesQuery{TenantID: tenantA})
		if err != nil || views == nil || len(views) != 0 {
			t.Errorf("got %#v, %v", views, err)
		}
	})

	t.Run("invalid tenant", func(t *testing.T) {
		_, err := NewListDeliveriesUseCase(newDeliveryRepo()).Execute(ctx, ListDeliveriesQuery{TenantID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		repo := newDeliveryRepo()
		repo.listErr = errBoom
		_, err := NewListDeliveriesUseCase(repo).Execute(ctx, ListDeliveriesQuery{TenantID: tenantA})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "list deliveries") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}

func TestGetDeliveryUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("returns delivery with attempts", func(t *testing.T) {
		d := deliveryWithAttempts(newEndpoint(tenantA))
		v, err := NewGetDeliveryUseCase(newDeliveryRepo(d)).Execute(ctx, GetDeliveryQuery{TenantID: tenantA, DeliveryID: d.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ID != d.ID().String() || len(v.Attempts) != 1 || v.Attempts[0].HTTPStatus != 500 {
			t.Errorf("view = %+v", v)
		}
	})

	t.Run("delivery of another tenant is reported as not found", func(t *testing.T) {
		d := deliveryWithAttempts(newEndpoint(tenantB))
		_, err := NewGetDeliveryUseCase(newDeliveryRepo(d)).Execute(ctx, GetDeliveryQuery{TenantID: tenantA, DeliveryID: d.ID().String()})
		if !errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected ErrDeliveryNotFound, got %v", err)
		}
	})

	t.Run("repo error is returned", func(t *testing.T) {
		repo := newDeliveryRepo()
		repo.findErr = errBoom
		_, err := NewGetDeliveryUseCase(repo).Execute(ctx, GetDeliveryQuery{TenantID: tenantA, DeliveryID: domain.NewDeliveryID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := NewGetDeliveryUseCase(newDeliveryRepo()).Execute(ctx, GetDeliveryQuery{TenantID: "x", DeliveryID: domain.NewDeliveryID().String()})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("invalid delivery id", func(t *testing.T) {
		_, err := NewGetDeliveryUseCase(newDeliveryRepo()).Execute(ctx, GetDeliveryQuery{TenantID: tenantA, DeliveryID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid delivery id") {
			t.Fatalf("expected invalid delivery error, got %v", err)
		}
	})
}

func TestListEndpointsUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("maps endpoints without exposing the secret", func(t *testing.T) {
		e := newEndpoint(tenantA, "payment.captured", "payout.confirmed")
		repo := newEndpointRepo()
		repo.byTenant = []*domain.WebhookEndpoint{e}

		views, err := NewListEndpointsUseCase(repo).Execute(ctx, tenantA)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := EndpointView{
			ID:          e.ID().String(),
			TenantID:    tenantA,
			URL:         "https://merchant.example.com/hooks",
			SecretHint:  "...3456",
			Events:      []string{"payment.captured", "payout.confirmed"},
			Active:      true,
			Description: "main",
			CreatedAt:   e.CreatedAt().Format(time.RFC3339),
		}
		if len(views) != 1 || !reflect.DeepEqual(views[0], want) {
			t.Errorf("views = %+v\nwant [%+v]", views, want)
		}
		if repo.lastTenant != domain.TenantID(tenantA) {
			t.Errorf("FindByTenant(%q)", repo.lastTenant)
		}
	})

	t.Run("invalid tenant", func(t *testing.T) {
		_, err := NewListEndpointsUseCase(newEndpointRepo()).Execute(ctx, "x")
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		repo := newEndpointRepo()
		repo.tenantErr = errBoom
		_, err := NewListEndpointsUseCase(repo).Execute(ctx, tenantA)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "list endpoints") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
