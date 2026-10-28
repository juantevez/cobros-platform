package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// seedOpenAlert crea una alerta abierta (four-eyes: sanctions_match/high) y la
// carga en el repo fake.
func seedOpenAlert(r *fakeAlertRepo, tenantID domain.TenantID) *domain.Alert {
	a := domain.NewAlert(domain.NewAlertID(), tenantID, domain.AlertSanctionsMatch,
		domain.RiskHigh, "subject", 95, nil, testNow)
	a.PullEvents()
	r.byID[a.ID()] = a
	return a
}

// seedSingleStepAlert crea una alerta de riesgo medio (sin four-eyes).
func seedSingleStepAlert(r *fakeAlertRepo, tenantID domain.TenantID) *domain.Alert {
	a := domain.NewAlert(domain.NewAlertID(), tenantID, domain.AlertTransactionThreshold,
		domain.RiskMedium, "pay-1", 70, nil, testNow)
	a.PullEvents()
	r.byID[a.ID()] = a
	return a
}

func TestResolveAlertUseCase_Execute(t *testing.T) {
	tid, _ := domain.ParseTenantID(validTenant())
	newUC := func(r *fakeAlertRepo, p *fakePublisher) *ResolveAlertUseCase {
		return NewResolveAlertUseCase(r, fakeTx{}, p, newClock())
	}

	t.Run("single-step alert is closed and published", func(t *testing.T) {
		r := newAlertRepo()
		a := seedSingleStepAlert(r, tid)
		p := &fakePublisher{}
		cmd := ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "cleared", Note: "fp", Analyst: "officer-1"}
		if err := newUC(r, p).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.updated) != 1 || r.updated[0].Status() != domain.StatusCleared || r.updated[0].ResolvedBy() != "officer-1" {
			t.Fatalf("alert not resolved as expected: %+v", r.updated)
		}
		if r.gotFrom != domain.StatusOpen {
			t.Errorf("conditional update from = %q, want open", r.gotFrom)
		}
		if len(p.published) != 1 || p.published[0].EventType() != "compliance.alert.resolved.v1" {
			t.Errorf("unexpected published events: %+v", p.published)
		}
	})

	t.Run("four-eyes alert becomes a proposal", func(t *testing.T) {
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		p := &fakePublisher{}
		cmd := ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "confirmed", Analyst: "officer-1"}
		if err := newUC(r, p).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.updated[0].Status() != domain.StatusPendingSecondApproval {
			t.Errorf("status = %q, want pending_second_approval", r.updated[0].Status())
		}
		if len(p.published) != 1 || p.published[0].EventType() != "compliance.alert.review_proposed.v1" {
			t.Errorf("unexpected published events: %+v", p.published)
		}
	})

	t.Run("alert of any tenant can be reviewed (platform)", func(t *testing.T) {
		r := newAlertRepo()
		other, _ := domain.ParseTenantID(validTenant())
		a := seedSingleStepAlert(r, other)
		cmd := ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "cleared", Analyst: "officer-1"}
		if err := newUC(r, &fakePublisher{}).Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid alert id rejected", func(t *testing.T) {
		cmd := ResolveAlertCmd{AlertID: "bad", Disposition: "cleared"}
		if err := newUC(newAlertRepo(), &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidAlertID) {
			t.Fatalf("expected ErrInvalidAlertID, got %v", err)
		}
	})

	t.Run("invalid disposition rejected", func(t *testing.T) {
		cmd := ResolveAlertCmd{AlertID: uuid.NewString(), Disposition: "maybe"}
		if err := newUC(newAlertRepo(), &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
	})

	t.Run("alert not found", func(t *testing.T) {
		cmd := ResolveAlertCmd{AlertID: uuid.NewString(), Disposition: "cleared"}
		if err := newUC(newAlertRepo(), &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})

	t.Run("already resolved returns ErrAlertNotOpen", func(t *testing.T) {
		r := newAlertRepo()
		a := seedSingleStepAlert(r, tid)
		_ = a.Resolve(domain.StatusConfirmed, "", "officer-1", testNow)
		a.PullEvents()
		cmd := ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "cleared", Analyst: "officer-2"}
		if err := newUC(r, &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotOpen) {
			t.Fatalf("expected ErrAlertNotOpen, got %v", err)
		}
	})

	t.Run("concurrent review (conditional update) publishes nothing", func(t *testing.T) {
		r := newAlertRepo()
		a := seedSingleStepAlert(r, tid)
		r.updateErr = domain.ErrAlertNotOpen // otro analista ganó la carrera
		p := &fakePublisher{}
		cmd := ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "confirmed", Analyst: "officer-1"}
		if err := newUC(r, p).Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotOpen) {
			t.Fatalf("expected ErrAlertNotOpen, got %v", err)
		}
		if len(p.published) != 0 {
			t.Errorf("expected no published events, got %d", len(p.published))
		}
	})

	t.Run("find and update errors propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.findErr = errBoom
		cmd := ResolveAlertCmd{AlertID: uuid.NewString(), Disposition: "cleared"}
		if err := newUC(r, &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("find: expected errBoom, got %v", err)
		}

		r = newAlertRepo()
		a := seedSingleStepAlert(r, tid)
		r.updateErr = errBoom
		cmd = ResolveAlertCmd{AlertID: a.ID().String(), Disposition: "confirmed", Analyst: "officer-1"}
		if err := newUC(r, &fakePublisher{}).Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("update: expected errBoom, got %v", err)
		}
	})
}

func TestReviewProposalUseCases(t *testing.T) {
	tid, _ := domain.ParseTenantID(validTenant())

	seedPending := func(r *fakeAlertRepo) *domain.Alert {
		a := seedOpenAlert(r, tid)
		_ = a.Resolve(domain.StatusConfirmed, "coincide", "officer-1", testNow)
		a.PullEvents()
		return a
	}

	t.Run("approve closes with the proposed disposition", func(t *testing.T) {
		r := newAlertRepo()
		a := seedPending(r)
		p := &fakePublisher{}
		uc := NewApproveAlertProposalUseCase(r, fakeTx{}, p, newClock())
		if err := uc.Execute(context.Background(), ReviewProposalCmd{AlertID: a.ID().String(), Note: "ok", Analyst: "officer-2"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.updated[0].Status() != domain.StatusConfirmed || r.updated[0].ResolvedBy() != "officer-2" {
			t.Errorf("unexpected state: %+v", r.updated[0])
		}
		if r.gotFrom != domain.StatusPendingSecondApproval {
			t.Errorf("conditional update from = %q", r.gotFrom)
		}
		if len(p.published) != 1 || p.published[0].EventType() != "compliance.alert.resolved.v1" {
			t.Errorf("unexpected published events: %+v", p.published)
		}
	})

	t.Run("approve by the proposer is rejected", func(t *testing.T) {
		r := newAlertRepo()
		a := seedPending(r)
		uc := NewApproveAlertProposalUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		err := uc.Execute(context.Background(), ReviewProposalCmd{AlertID: a.ID().String(), Analyst: "officer-1"})
		if !errors.Is(err, domain.ErrSameAnalyst) {
			t.Fatalf("expected ErrSameAnalyst, got %v", err)
		}
		if len(r.updated) != 0 {
			t.Error("nothing should be persisted")
		}
	})

	t.Run("reject sends the alert back to open", func(t *testing.T) {
		r := newAlertRepo()
		a := seedPending(r)
		p := &fakePublisher{}
		uc := NewRejectAlertProposalUseCase(r, fakeTx{}, p, newClock())
		if err := uc.Execute(context.Background(), ReviewProposalCmd{AlertID: a.ID().String(), Note: "no", Analyst: "officer-2"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.updated[0].Status() != domain.StatusOpen {
			t.Errorf("status = %q, want open", r.updated[0].Status())
		}
		if len(p.published) != 1 || p.published[0].EventType() != "compliance.alert.proposal_rejected.v1" {
			t.Errorf("unexpected published events: %+v", p.published)
		}
	})

	t.Run("approve/reject without proposal", func(t *testing.T) {
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		approve := NewApproveAlertProposalUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		reject := NewRejectAlertProposalUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := ReviewProposalCmd{AlertID: a.ID().String(), Analyst: "officer-2"}
		if err := approve.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotPendingApproval) {
			t.Fatalf("approve: expected ErrAlertNotPendingApproval, got %v", err)
		}
		if err := reject.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotPendingApproval) {
			t.Fatalf("reject: expected ErrAlertNotPendingApproval, got %v", err)
		}
	})
}
