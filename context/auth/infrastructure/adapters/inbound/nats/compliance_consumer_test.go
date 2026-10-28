package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

type fakeHoldRepo struct {
	placed, released []string
	err              error
}

func (r *fakeHoldRepo) Place(ctx context.Context, tid domain.TenantID, alertID string, at time.Time) error {
	r.placed = append(r.placed, alertID)
	return r.err
}

func (r *fakeHoldRepo) Release(ctx context.Context, tid domain.TenantID, alertID string, at time.Time) error {
	r.released = append(r.released, alertID)
	return r.err
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }

func newActiveTenant(t *testing.T) *domain.Tenant {
	t.Helper()
	tenant := newPendingTenant(t)
	if err := tenant.Activate(domain.EnvironmentProduction); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tenant.PullEvents()
	return tenant
}

func newComplianceConsumer(holds *fakeHoldRepo, repo *fakeTenantRepo) (*ComplianceConsumer, *fakeConsumer, *fakePublisher) {
	fc := &fakeConsumer{}
	pub := &fakePublisher{}
	return NewComplianceConsumer(fc,
		application.NewPlaceComplianceHoldUseCase(holds, fixedClock{}),
		application.NewReleaseComplianceHoldUseCase(holds, fixedClock{}),
		application.NewSuspendForComplianceUseCase(application.NewSuspendTenantUseCase(repo, fakeTx{}, pub)),
		discardLogger(),
	), fc, pub
}

func sanctionMsg(subject, tenantID, alertID string) *eventbus.Message {
	return &eventbus.Message{
		Subject: subject,
		Payload: []byte(`{"tenant_id":"` + tenantID + `","alert_id":"` + alertID + `"}`),
	}
}

func TestComplianceConsumer_Start(t *testing.T) {
	c, fc, _ := newComplianceConsumer(&fakeHoldRepo{}, newFakeTenantRepo())
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.gotCfg.Stream != "COMPLIANCE" || fc.gotCfg.FilterSubject != "compliance.sanction.>" ||
		fc.gotCfg.Name != "auth-compliance-consumer" {
		t.Errorf("unexpected config: %+v", fc.gotCfg)
	}
}

func TestComplianceConsumer_Handle(t *testing.T) {
	tenant := newActiveTenant(t)
	alertID := uuid.NewString()

	t.Run("matched places the hold", func(t *testing.T) {
		holds := &fakeHoldRepo{}
		c, _, _ := newComplianceConsumer(holds, newFakeTenantRepo(tenant))
		if err := c.handle(context.Background(), sanctionMsg("compliance.sanction.matched.v1", tenant.ID().String(), alertID)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(holds.placed) != 1 || holds.placed[0] != alertID {
			t.Errorf("placed = %v", holds.placed)
		}
	})

	t.Run("cleared releases the hold", func(t *testing.T) {
		holds := &fakeHoldRepo{}
		c, _, _ := newComplianceConsumer(holds, newFakeTenantRepo(tenant))
		if err := c.handle(context.Background(), sanctionMsg("compliance.sanction.cleared.v1", tenant.ID().String(), alertID)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(holds.released) != 1 || holds.released[0] != alertID {
			t.Errorf("released = %v", holds.released)
		}
	})

	t.Run("confirmed suspends with generic reason", func(t *testing.T) {
		tn := newActiveTenant(t)
		repo := newFakeTenantRepo(tn)
		c, _, pub := newComplianceConsumer(&fakeHoldRepo{}, repo)
		if err := c.handle(context.Background(), sanctionMsg("compliance.sanction.confirmed.v1", tn.ID().String(), alertID)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.tenants[tn.ID()].Status() != domain.TenantStatusSuspended {
			t.Fatal("tenant should be suspended")
		}
		if ev := pub.published[0].(domain.TenantSuspendedEvent); ev.Reason != domain.ComplianceReviewReason {
			t.Errorf("reason = %q", ev.Reason)
		}
	})

	t.Run("malformed or incomplete payload is acked", func(t *testing.T) {
		holds := &fakeHoldRepo{}
		c, _, _ := newComplianceConsumer(holds, newFakeTenantRepo())
		for _, payload := range []string{`{bad`, `{"tenant_id":"x"}`, `{"alert_id":"x"}`} {
			msg := &eventbus.Message{Subject: "compliance.sanction.matched.v1", Payload: []byte(payload)}
			if err := c.handle(context.Background(), msg); err != nil {
				t.Errorf("payload %s should be acked, got %v", payload, err)
			}
		}
		if len(holds.placed) != 0 {
			t.Error("nothing should be placed")
		}
	})

	t.Run("unknown subject is acked", func(t *testing.T) {
		c, _, _ := newComplianceConsumer(&fakeHoldRepo{}, newFakeTenantRepo())
		if err := c.handle(context.Background(), sanctionMsg("compliance.sanction.other.v2", tenant.ID().String(), alertID)); err != nil {
			t.Fatalf("expected ack, got %v", err)
		}
	})

	t.Run("repo error naks for retry", func(t *testing.T) {
		c, _, _ := newComplianceConsumer(&fakeHoldRepo{err: errors.New("boom")}, newFakeTenantRepo())
		if err := c.handle(context.Background(), sanctionMsg("compliance.sanction.matched.v1", tenant.ID().String(), alertID)); err == nil {
			t.Fatal("expected error for nak")
		}
	})
}
