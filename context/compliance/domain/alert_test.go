package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testTenantID(t *testing.T) TenantID {
	t.Helper()
	tid, err := ParseTenantID(uuid.NewString())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return tid
}

func sanctionsDetails(listType string) map[string]string {
	return map[string]string{
		"matched_name": "Vladimir Petrov", "list_type": listType, "source": "OFAC",
	}
}

func eventTypes(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventType()
	}
	return out
}

func TestNewAlert(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	tid := testTenantID(t)
	id := NewAlertID()

	t.Run("creates open alert and emits AlertRaisedEvent", func(t *testing.T) {
		a := NewAlert(id, tid, AlertSanctionsMatch, RiskHigh, "Osama Bin Laden", 95,
			map[string]string{"list": "OFAC"}, now)

		if a.ID() != id || a.TenantID() != tid {
			t.Errorf("id/tenant mismatch: %+v", a)
		}
		if a.Status() != StatusOpen {
			t.Errorf("status = %q, want open", a.Status())
		}
		if a.Type() != AlertSanctionsMatch || a.RiskLevel() != RiskHigh {
			t.Errorf("type/risk mismatch")
		}
		if a.Subject() != "Osama Bin Laden" || a.Score() != 95 {
			t.Errorf("subject/score mismatch")
		}
		if a.Details()["list"] != "OFAC" {
			t.Errorf("details mismatch: %+v", a.Details())
		}
		if !a.CreatedAt().Equal(now) || a.ResolvedAt() != nil {
			t.Errorf("timestamps mismatch: %v / %v", a.CreatedAt(), a.ResolvedAt())
		}

		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %v", eventTypes(events))
		}
		raised, ok := events[0].(AlertRaisedEvent)
		if !ok {
			t.Fatalf("expected AlertRaisedEvent, got %T", events[0])
		}
		if raised.AlertID != id.String() || raised.TenantID != tid.String() ||
			raised.AlertType != "sanctions_match" || raised.RiskLevel != "high" ||
			raised.Subject != "Osama Bin Laden" || raised.Score != 95 {
			t.Errorf("event mismatch: %+v", raised)
		}
	})

	t.Run("exact sanctions-list match also emits SanctionMatchedEvent", func(t *testing.T) {
		a := NewAlert(id, tid, AlertSanctionsMatch, RiskHigh, "Vladimir Petrov", ScoreExactMatch,
			sanctionsDetails(ListTypeSanctions), now)
		events := a.PullEvents()
		if len(events) != 2 {
			t.Fatalf("expected raised + sanction.matched, got %v", eventTypes(events))
		}
		m, ok := events[1].(SanctionMatchedEvent)
		if !ok {
			t.Fatalf("expected SanctionMatchedEvent, got %T", events[1])
		}
		if m.AlertID != id.String() || m.TenantID != tid.String() || m.MatchedName != "Vladimir Petrov" ||
			m.ListType != "sanctions" || m.Source != "OFAC" || m.Score != 100 {
			t.Errorf("event mismatch: %+v", m)
		}
	})

	t.Run("no hold for partial match, PEP list or transaction rules", func(t *testing.T) {
		cases := []*Alert{
			NewAlert(id, tid, AlertSanctionsMatch, RiskHigh, "s", ScoreOrderedMatch, sanctionsDetails(ListTypeSanctions), now),
			NewAlert(id, tid, AlertSanctionsMatch, RiskHigh, "s", ScoreExactMatch, sanctionsDetails(ListTypePEP), now),
			NewAlert(id, tid, AlertTransactionVolume, RiskHigh, "s", 100, nil, now),
		}
		for i, a := range cases {
			if evs := a.PullEvents(); len(evs) != 1 {
				t.Errorf("case %d: expected only raised, got %v", i, eventTypes(evs))
			}
		}
	})

	t.Run("nil details defaults to empty map", func(t *testing.T) {
		a := NewAlert(NewAlertID(), tid, AlertTransactionThreshold, RiskMedium, "pay-1", 70, nil, now)
		if a.Details() == nil || len(a.Details()) != 0 {
			t.Errorf("expected empty details, got %+v", a.Details())
		}
	})
}

func TestAlert_RequiresSecondApproval(t *testing.T) {
	tid := testTenantID(t)
	now := time.Now()
	cases := []struct {
		typ  AlertType
		risk RiskLevel
		want bool
	}{
		{AlertSanctionsMatch, RiskMedium, true},
		{AlertTransactionVelocity, RiskHigh, true},
		{AlertTransactionThreshold, RiskMedium, false},
		{AlertTransactionThreshold, RiskLow, false},
	}
	for _, c := range cases {
		a := NewAlert(NewAlertID(), tid, c.typ, c.risk, "s", 70, nil, now)
		if got := a.RequiresSecondApproval(); got != c.want {
			t.Errorf("%s/%s: RequiresSecondApproval = %v, want %v", c.typ, c.risk, got, c.want)
		}
	}
}

func TestAlert_Resolve_singleStep(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	resolveTime := now.Add(time.Hour)
	tid := testTenantID(t)

	newOpen := func() *Alert {
		a := NewAlert(NewAlertID(), tid, AlertTransactionThreshold, RiskMedium, "pay-1", 70, nil, now)
		a.PullEvents()
		return a
	}

	t.Run("medium risk closes in one step", func(t *testing.T) {
		a := newOpen()
		if err := a.Resolve(StatusCleared, "falso positivo", "officer-1", resolveTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusCleared || a.Note() != "falso positivo" || a.ResolvedBy() != "officer-1" {
			t.Errorf("unexpected state: %+v", a)
		}
		if a.ResolvedAt() == nil || !a.ResolvedAt().Equal(resolveTime) {
			t.Errorf("resolvedAt = %v, want %v", a.ResolvedAt(), resolveTime)
		}
		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %v", eventTypes(events))
		}
		resolved, ok := events[0].(AlertResolvedEvent)
		if !ok || resolved.Status != "cleared" || resolved.ResolvedBy != "officer-1" || resolved.ProposedBy != "" {
			t.Errorf("event mismatch: %+v", events[0])
		}
	})

	t.Run("open is not a terminal disposition", func(t *testing.T) {
		a := newOpen()
		if err := a.Resolve(StatusOpen, "", "officer-1", resolveTime); !errors.Is(err, ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
		if a.Status() != StatusOpen || len(a.PullEvents()) != 0 {
			t.Error("failed resolve must not mutate nor emit")
		}
	})

	t.Run("cannot resolve twice", func(t *testing.T) {
		a := newOpen()
		_ = a.Resolve(StatusCleared, "", "officer-1", resolveTime)
		a.PullEvents()
		if err := a.Resolve(StatusConfirmed, "", "officer-1", resolveTime); !errors.Is(err, ErrAlertNotOpen) {
			t.Fatalf("expected ErrAlertNotOpen, got %v", err)
		}
		if a.Status() != StatusCleared || len(a.PullEvents()) != 0 {
			t.Error("failed resolve must not mutate nor emit")
		}
	})
}

func TestAlert_FourEyes(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	tid := testTenantID(t)

	newPending := func(t *testing.T, details map[string]string) *Alert {
		t.Helper()
		a := NewAlert(NewAlertID(), tid, AlertSanctionsMatch, RiskHigh, "Vladimir Petrov", ScoreExactMatch, details, now)
		a.PullEvents()
		if err := a.Resolve(StatusConfirmed, "coincide DNI", "officer-1", now); err != nil {
			t.Fatalf("propose: %v", err)
		}
		return a
	}

	t.Run("first resolution becomes a proposal", func(t *testing.T) {
		a := newPending(t, nil)
		if a.Status() != StatusPendingSecondApproval {
			t.Fatalf("status = %q, want pending_second_approval", a.Status())
		}
		if a.ProposedStatus() != StatusConfirmed || a.ProposedBy() != "officer-1" ||
			a.ProposedAt() == nil || a.Note() != "coincide DNI" {
			t.Errorf("proposal not recorded: %+v", a)
		}
		if a.ResolvedAt() != nil || a.ResolvedBy() != "" {
			t.Error("proposal must not close the alert")
		}
		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %v", eventTypes(events))
		}
		p, ok := events[0].(AlertReviewProposedEvent)
		if !ok || p.ProposedStatus != "confirmed" || p.ProposedBy != "officer-1" {
			t.Errorf("event mismatch: %+v", events[0])
		}
	})

	t.Run("pending alert cannot be resolved again", func(t *testing.T) {
		a := newPending(t, nil)
		if err := a.Resolve(StatusCleared, "", "officer-2", later); !errors.Is(err, ErrAlertPendingApproval) {
			t.Fatalf("expected ErrAlertPendingApproval, got %v", err)
		}
	})

	t.Run("second analyst approves and closes", func(t *testing.T) {
		a := newPending(t, nil)
		a.PullEvents()
		if err := a.ApproveProposal("de acuerdo", "officer-2", later); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusConfirmed || a.ResolvedBy() != "officer-2" || a.ProposedBy() != "officer-1" ||
			a.ApprovalNote() != "de acuerdo" || a.Note() != "coincide DNI" {
			t.Errorf("unexpected state: %+v", a)
		}
		if a.ResolvedAt() == nil || !a.ResolvedAt().Equal(later) {
			t.Errorf("resolvedAt = %v", a.ResolvedAt())
		}
		events := a.PullEvents()
		r, ok := events[0].(AlertResolvedEvent)
		if !ok || r.ResolvedBy != "officer-2" || r.ProposedBy != "officer-1" || r.Status != "confirmed" {
			t.Errorf("event mismatch: %+v", events[0])
		}
	})

	t.Run("same analyst cannot approve or reject own proposal", func(t *testing.T) {
		a := newPending(t, nil)
		a.PullEvents()
		if err := a.ApproveProposal("", "officer-1", later); !errors.Is(err, ErrSameAnalyst) {
			t.Fatalf("approve: expected ErrSameAnalyst, got %v", err)
		}
		if err := a.RejectProposal("", "officer-1", later); !errors.Is(err, ErrSameAnalyst) {
			t.Fatalf("reject: expected ErrSameAnalyst, got %v", err)
		}
		if err := a.ApproveProposal("", "", later); !errors.Is(err, ErrSameAnalyst) {
			t.Fatalf("anonymous: expected ErrSameAnalyst, got %v", err)
		}
		if a.Status() != StatusPendingSecondApproval || len(a.PullEvents()) != 0 {
			t.Error("rejected attempts must not mutate nor emit")
		}
	})

	t.Run("second analyst rejects: back to open without proposal", func(t *testing.T) {
		a := newPending(t, nil)
		a.PullEvents()
		if err := a.RejectProposal("falta evidencia", "officer-2", later); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusOpen || a.ProposedStatus() != "" || a.ProposedBy() != "" ||
			a.ProposedAt() != nil || a.Note() != "" {
			t.Errorf("proposal not cleared: %+v", a)
		}
		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %v", eventTypes(events))
		}
		r, ok := events[0].(AlertProposalRejectedEvent)
		if !ok || r.ProposedStatus != "confirmed" || r.ProposedBy != "officer-1" ||
			r.RejectedBy != "officer-2" || r.Note != "falta evidencia" {
			t.Errorf("event mismatch: %+v", events[0])
		}
		// Tras el rechazo, un nuevo ciclo four-eyes es posible.
		if err := a.Resolve(StatusCleared, "homónimo", "officer-2", later); err != nil {
			t.Fatalf("re-propose: %v", err)
		}
		if a.Status() != StatusPendingSecondApproval || a.ProposedBy() != "officer-2" {
			t.Errorf("unexpected state: %+v", a)
		}
	})

	t.Run("approve/reject without pending proposal", func(t *testing.T) {
		a := NewAlert(NewAlertID(), tid, AlertSanctionsMatch, RiskHigh, "s", 90, nil, now)
		if err := a.ApproveProposal("", "officer-2", later); !errors.Is(err, ErrAlertNotPendingApproval) {
			t.Fatalf("expected ErrAlertNotPendingApproval, got %v", err)
		}
		if err := a.RejectProposal("", "officer-2", later); !errors.Is(err, ErrAlertNotPendingApproval) {
			t.Fatalf("expected ErrAlertNotPendingApproval, got %v", err)
		}
	})

	t.Run("sanctions-list confirmation emits SanctionConfirmedEvent", func(t *testing.T) {
		a := newPending(t, sanctionsDetails(ListTypeSanctions))
		a.PullEvents()
		_ = a.ApproveProposal("", "officer-2", later)
		events := a.PullEvents()
		if len(events) != 2 {
			t.Fatalf("expected resolved + sanction.confirmed, got %v", eventTypes(events))
		}
		if c, ok := events[1].(SanctionConfirmedEvent); !ok || c.AlertID != a.ID().String() || c.TenantID != tid.String() {
			t.Errorf("event mismatch: %+v", events[1])
		}
	})

	t.Run("sanctions-list clearance emits SanctionClearedEvent", func(t *testing.T) {
		a := NewAlert(NewAlertID(), tid, AlertSanctionsMatch, RiskHigh, "s", ScoreExactMatch,
			sanctionsDetails(ListTypeSanctions), now)
		a.PullEvents()
		_ = a.Resolve(StatusCleared, "homónimo", "officer-1", now)
		_ = a.ApproveProposal("", "officer-2", later)
		events := a.PullEvents()
		if got := eventTypes(events); len(got) != 3 || got[2] != "compliance.sanction.cleared.v1" {
			t.Fatalf("unexpected events: %v", got)
		}
	})

	t.Run("PEP list closure has no enforcement events", func(t *testing.T) {
		a := newPending(t, sanctionsDetails(ListTypePEP))
		a.PullEvents()
		_ = a.ApproveProposal("", "officer-2", later)
		if evs := a.PullEvents(); len(evs) != 1 {
			t.Errorf("expected only resolved, got %v", eventTypes(evs))
		}
	})
}

func TestReconstituteAlert(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resolved := now.Add(2 * time.Hour)
	proposed := now.Add(time.Hour)
	tid := testTenantID(t)
	id := NewAlertID()

	a := ReconstituteAlert(AlertSnapshot{
		ID: id, TenantID: tid, Type: AlertTransactionVelocity, RiskLevel: RiskHigh,
		Status: StatusConfirmed, Subject: "pay-9", Score: 80,
		Details: map[string]string{"rule": "velocity"}, Note: "confirmado",
		ResolvedBy: "officer-2", CreatedAt: now, ResolvedAt: &resolved,
		ProposedStatus: StatusConfirmed, ProposedBy: "officer-1", ProposedAt: &proposed,
		ApprovalNote: "ok",
	})

	if a.ID() != id || a.Status() != StatusConfirmed || a.Type() != AlertTransactionVelocity {
		t.Errorf("fields not restored: %+v", a)
	}
	if a.Note() != "confirmado" || a.Score() != 80 || a.ResolvedBy() != "officer-2" {
		t.Errorf("note/score/resolvedBy not restored")
	}
	if a.ProposedStatus() != StatusConfirmed || a.ProposedBy() != "officer-1" ||
		a.ProposedAt() == nil || a.ApprovalNote() != "ok" {
		t.Errorf("four-eyes fields not restored: %+v", a)
	}
	if a.ResolvedAt() == nil || !a.ResolvedAt().Equal(resolved) || a.Details()["rule"] != "velocity" {
		t.Errorf("resolvedAt/details not restored")
	}
	if len(a.PullEvents()) != 0 {
		t.Error("reconstitute must not emit events")
	}
}

func TestReconstituteAlert_nilDetails(t *testing.T) {
	a := ReconstituteAlert(AlertSnapshot{ID: NewAlertID(), TenantID: testTenantID(t), Status: StatusOpen})
	if a.Details() == nil {
		t.Error("details should default to empty map, not nil")
	}
}
