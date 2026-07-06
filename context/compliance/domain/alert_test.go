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
		if !a.CreatedAt().Equal(now) {
			t.Errorf("createdAt mismatch: %v", a.CreatedAt())
		}
		if a.ResolvedAt() != nil {
			t.Errorf("resolvedAt should be nil, got %v", a.ResolvedAt())
		}

		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		raised, ok := events[0].(AlertRaisedEvent)
		if !ok {
			t.Fatalf("expected AlertRaisedEvent, got %T", events[0])
		}
		if raised.AlertID != id.String() || raised.TenantID != tid.String() {
			t.Errorf("event ids mismatch: %+v", raised)
		}
		if raised.AlertType != "sanctions_match" || raised.RiskLevel != "high" {
			t.Errorf("event type/risk mismatch: %+v", raised)
		}
		if raised.Subject != "Osama Bin Laden" || raised.Score != 95 {
			t.Errorf("event subject/score mismatch: %+v", raised)
		}
	})

	t.Run("nil details defaults to empty map", func(t *testing.T) {
		a := NewAlert(NewAlertID(), tid, AlertTransactionThreshold, RiskMedium, "pay-1", 70, nil, now)
		if a.Details() == nil {
			t.Fatal("details should not be nil")
		}
		if len(a.Details()) != 0 {
			t.Errorf("expected empty details, got %+v", a.Details())
		}
	})
}

func TestAlert_Resolve(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	resolveTime := now.Add(time.Hour)
	tid := testTenantID(t)

	newOpen := func() *Alert {
		a := NewAlert(NewAlertID(), tid, AlertSanctionsMatch, RiskHigh, "subj", 90, nil, now)
		a.PullEvents() // descartar el raised
		return a
	}

	t.Run("cleared from open", func(t *testing.T) {
		a := newOpen()
		if err := a.Resolve(StatusCleared, "falso positivo", resolveTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusCleared {
			t.Errorf("status = %q, want cleared", a.Status())
		}
		if a.Note() != "falso positivo" {
			t.Errorf("note = %q", a.Note())
		}
		if a.ResolvedAt() == nil || !a.ResolvedAt().Equal(resolveTime) {
			t.Errorf("resolvedAt = %v, want %v", a.ResolvedAt(), resolveTime)
		}

		events := a.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		resolved, ok := events[0].(AlertResolvedEvent)
		if !ok {
			t.Fatalf("expected AlertResolvedEvent, got %T", events[0])
		}
		if resolved.Status != "cleared" || resolved.AlertID != a.ID().String() {
			t.Errorf("event mismatch: %+v", resolved)
		}
	})

	t.Run("confirmed from open", func(t *testing.T) {
		a := newOpen()
		if err := a.Resolve(StatusConfirmed, "verdadero", resolveTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Status() != StatusConfirmed {
			t.Errorf("status = %q, want confirmed", a.Status())
		}
	})

	t.Run("cannot resolve twice", func(t *testing.T) {
		a := newOpen()
		if err := a.Resolve(StatusCleared, "", resolveTime); err != nil {
			t.Fatalf("first resolve failed: %v", err)
		}
		a.PullEvents()

		err := a.Resolve(StatusConfirmed, "", resolveTime)
		if !errors.Is(err, ErrAlertNotOpen) {
			t.Fatalf("expected ErrAlertNotOpen, got %v", err)
		}
		// El estado no debe cambiar tras el intento fallido.
		if a.Status() != StatusCleared {
			t.Errorf("status changed to %q after failed resolve", a.Status())
		}
		if len(a.PullEvents()) != 0 {
			t.Error("no event should be emitted on failed resolve")
		}
	})
}

func TestReconstituteAlert(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resolved := now.Add(2 * time.Hour)
	tid := testTenantID(t)
	id := NewAlertID()

	a := ReconstituteAlert(id, tid, AlertTransactionVelocity, RiskMedium, StatusConfirmed,
		"pay-9", 80, map[string]string{"rule": "velocity"}, "confirmado", now, &resolved)

	if a.ID() != id || a.Status() != StatusConfirmed || a.Type() != AlertTransactionVelocity {
		t.Errorf("fields not restored: %+v", a)
	}
	if a.Note() != "confirmado" || a.Score() != 80 {
		t.Errorf("note/score not restored")
	}
	if a.ResolvedAt() == nil || !a.ResolvedAt().Equal(resolved) {
		t.Errorf("resolvedAt not restored: %v", a.ResolvedAt())
	}
	if a.Details()["rule"] != "velocity" {
		t.Errorf("details not restored: %+v", a.Details())
	}
	if len(a.PullEvents()) != 0 {
		t.Error("reconstitute must not emit events")
	}
}

func TestReconstituteAlert_nilDetails(t *testing.T) {
	tid := testTenantID(t)
	a := ReconstituteAlert(NewAlertID(), tid, AlertSanctionsMatch, RiskLow, StatusOpen,
		"s", 10, nil, "", time.Now(), nil)
	if a.Details() == nil {
		t.Error("details should default to empty map, not nil")
	}
}
