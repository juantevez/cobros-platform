package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

type holdCall struct {
	tenantID domain.TenantID
	alertID  string
	at       time.Time
}

type fakeHoldRepo struct {
	placed, released []holdCall
	err              error
}

func (r *fakeHoldRepo) Place(ctx context.Context, tenantID domain.TenantID, alertID string, at time.Time) error {
	r.placed = append(r.placed, holdCall{tenantID, alertID, at})
	return r.err
}

func (r *fakeHoldRepo) Release(ctx context.Context, tenantID domain.TenantID, alertID string, at time.Time) error {
	r.released = append(r.released, holdCall{tenantID, alertID, at})
	return r.err
}

func TestComplianceHoldUseCases(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	clock := fakeClock{now: now}
	tenantID := domain.NewTenantID()
	alertID := uuid.NewString()
	cmd := ComplianceHoldCmd{TenantID: tenantID.String(), AlertID: alertID}

	t.Run("place records the hold", func(t *testing.T) {
		repo := &fakeHoldRepo{}
		if err := NewPlaceComplianceHoldUseCase(repo, clock).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.placed) != 1 || repo.placed[0] != (holdCall{tenantID, alertID, now}) {
			t.Errorf("placed = %+v", repo.placed)
		}
	})

	t.Run("release lifts the hold", func(t *testing.T) {
		repo := &fakeHoldRepo{}
		if err := NewReleaseComplianceHoldUseCase(repo, clock).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.released) != 1 || repo.released[0] != (holdCall{tenantID, alertID, now}) {
			t.Errorf("released = %+v", repo.released)
		}
	})

	t.Run("invalid ids rejected before touching the repo", func(t *testing.T) {
		repo := &fakeHoldRepo{}
		place := NewPlaceComplianceHoldUseCase(repo, clock)
		if err := place.Execute(context.Background(), ComplianceHoldCmd{TenantID: "bad", AlertID: alertID}); err == nil {
			t.Error("expected error for invalid tenant")
		}
		if err := place.Execute(context.Background(), ComplianceHoldCmd{TenantID: tenantID.String(), AlertID: "bad"}); err == nil {
			t.Error("expected error for invalid alert")
		}
		if len(repo.placed) != 0 {
			t.Error("repo should not be called")
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		repo := &fakeHoldRepo{err: errors.New("boom")}
		if err := NewPlaceComplianceHoldUseCase(repo, clock).Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestSuspendForComplianceUseCase(t *testing.T) {
	newUC := func(repo *fakeTenantRepo, pub *fakePublisher) *SuspendForComplianceUseCase {
		return NewSuspendForComplianceUseCase(NewSuspendTenantUseCase(repo, fakeTx{}, pub))
	}

	t.Run("suspends with the generic reason (anti tipping-off)", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		repo := newFakeTenantRepo(tenant)
		pub := &fakePublisher{}
		cmd := ComplianceHoldCmd{TenantID: tenant.ID().String(), AlertID: uuid.NewString()}
		if err := newUC(repo, pub).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.tenants[tenant.ID()].Status() != domain.TenantStatusSuspended {
			t.Fatalf("status = %q, want suspended", repo.tenants[tenant.ID()].Status())
		}
		ev, ok := pub.published[0].(domain.TenantSuspendedEvent)
		if !ok || ev.Reason != domain.ComplianceReviewReason {
			t.Errorf("suspension event must carry the generic reason, got %+v", pub.published[0])
		}
	})

	t.Run("already suspended is a no-op", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		_ = tenant.Suspend("previa")
		tenant.PullEvents()
		repo := newFakeTenantRepo(tenant)
		pub := &fakePublisher{}
		cmd := ComplianceHoldCmd{TenantID: tenant.ID().String(), AlertID: uuid.NewString()}
		if err := newUC(repo, pub).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("expected no-op, got %v", err)
		}
		if len(pub.published) != 0 {
			t.Errorf("no event expected, got %d", len(pub.published))
		}
	})

	t.Run("unknown tenant propagates error (nak)", func(t *testing.T) {
		cmd := ComplianceHoldCmd{TenantID: domain.NewTenantID().String(), AlertID: uuid.NewString()}
		if err := newUC(newFakeTenantRepo(), &fakePublisher{}).Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid alert id rejected", func(t *testing.T) {
		tenant := newActiveTenant(t, domain.EnvironmentProduction)
		cmd := ComplianceHoldCmd{TenantID: tenant.ID().String(), AlertID: "bad"}
		if err := newUC(newFakeTenantRepo(tenant), &fakePublisher{}).Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})
}
