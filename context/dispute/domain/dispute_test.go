package domain

import (
	"errors"
	"testing"
	"time"
)

const testTenant = "11111111-1111-1111-1111-111111111111"

func tenantID(t *testing.T) TenantID {
	t.Helper()
	return TenantID(testTenant)
}

// capturedPayment devuelve un PaymentRef capturado suficiente para crear
// disputas de hasta 5000 centavos en ARS dentro del mismo tenant.
func capturedPayment() PaymentRef {
	return PaymentRef{ID: "pay-1", TenantID: testTenant, Status: "captured", Amount: 5000, Currency: "ARS"}
}

// openDispute crea una disputa abierta con deadline en el futuro y descarta
// el evento DisputeOpenedEvent.
func openDispute(t *testing.T, deadline time.Time) *Dispute {
	t.Helper()
	now := time.Now()
	d, err := NewDispute(NewDisputeID(), capturedPayment(), "psp-1", 5000, "ARS", ReasonFraudulent, deadline, now)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}
	d.PullEvents()
	return d
}

func someEvidence(t *testing.T) []Evidence {
	t.Helper()
	ev, err := NewEvidence(NewEvidenceID(), "receipt", "s3://r", "desc", time.Now())
	if err != nil {
		t.Fatalf("new evidence: %v", err)
	}
	return []Evidence{ev}
}

// overdueDispute reconstituye una disputa open con deadline ya vencido:
// NewDispute rechaza deadlines pasados, así que se construye como vendría
// del repositorio.
func overdueDispute(t *testing.T, past time.Time) *Dispute {
t.Helper()
return ReconstituteDispute(NewDisputeID(), tenantID(t), "pay-1", "psp-1", 5000, "ARS",
ReasonFraudulent, StatusOpen, nil, "", "", past, past.Add(-time.Hour), nil, nil, 1)
}

func TestNewDispute(t *testing.T) {
	deadline := time.Now().Add(7 * 24 * time.Hour)

	t.Run("valid creates open dispute and emits opened event", func(t *testing.T) {
		id := NewDisputeID()
		tid := tenantID(t)
		now := time.Now()
		d, err := NewDispute(id, capturedPayment(), "psp-9", 5000, "ARS", ReasonDuplicate, deadline, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ID() != id || d.Status() != StatusOpen || d.TenantID() != tid {
			t.Errorf("id/status/tenant mismatch: %+v", d)
		}
		if d.PaymentID() != "pay-1" || d.PSPReference() != "psp-9" {
			t.Errorf("payment/psp mismatch")
		}
		if d.Amount() != 5000 || d.Currency() != "ARS" || d.Reason() != ReasonDuplicate {
			t.Errorf("amount/currency/reason mismatch")
		}
		if !d.Deadline().Equal(deadline) {
			t.Errorf("deadline mismatch")
		}
		if d.OpenedAt().Location() != time.UTC {
			t.Errorf("openedAt not UTC")
		}
		if d.RespondedAt() != nil || d.ResolvedAt() != nil {
			t.Errorf("responded/resolved should be nil")
		}

		events := d.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		opened, ok := events[0].(DisputeOpenedEvent)
		if !ok {
			t.Fatalf("expected DisputeOpenedEvent, got %T", events[0])
		}
		if opened.DisputeID != id.String() || opened.PaymentID != "pay-1" {
			t.Errorf("event ids mismatch: %+v", opened)
		}
		if opened.Amount != 5000 || opened.Currency != "ARS" || opened.Reason != "duplicate" {
			t.Errorf("event payload mismatch: %+v", opened)
		}
		// El comercio necesita el plazo (notificación y webhook).
		if !opened.Deadline.Equal(deadline) || opened.Deadline.Location() != time.UTC {
			t.Errorf("event deadline = %v, want %v in UTC", opened.Deadline, deadline)
		}
	})

	// La disputa es del dueño del pago: no hay tenant del caller que reasignar.
	t.Run("tenant comes from the payment", func(t *testing.T) {
		p := capturedPayment()
		p.TenantID = "22222222-2222-2222-2222-222222222222"
		d, err := NewDispute(NewDisputeID(), p, "psp", 5000, "ARS", ReasonGeneral, deadline, time.Now())
		if err != nil || d.TenantID().String() != p.TenantID {
			t.Fatalf("tenant = %v, %v; want %s", d, err, p.TenantID)
		}
	})

	t.Run("payment without a valid tenant rejected", func(t *testing.T) {
		for _, tenant := range []string{"", "tenant-1"} {
			p := capturedPayment()
			p.TenantID = tenant
			if _, err := NewDispute(NewDisputeID(), p, "psp", 5000, "ARS", ReasonGeneral, deadline, time.Now()); err == nil {
				t.Errorf("payment tenant %q: expected error", tenant)
			}
		}
	})

	t.Run("empty payment id rejected", func(t *testing.T) {
		emptyPayment := capturedPayment()
		emptyPayment.ID = ""
		_, err := NewDispute(NewDisputeID(), emptyPayment, "psp", 5000, "ARS", ReasonGeneral, deadline, time.Now())
		if err == nil {
			t.Fatal("expected error for empty payment id")
		}
	})

	t.Run("non-positive amount rejected", func(t *testing.T) {
		for _, amt := range []int64{0, -100} {
			_, err := NewDispute(NewDisputeID(), capturedPayment(), "psp", amt, "ARS", ReasonGeneral, deadline, time.Now())
			if err == nil {
				t.Errorf("expected error for amount %d", amt)
			}
		}
	})
}

func TestDispute_Contest(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)

	t.Run("valid contest moves to under_review", func(t *testing.T) {
		d := openDispute(t, future)
		now := time.Now()
		if err := d.Contest(someEvidence(t), "aquí está la prueba", now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusUnderReview {
			t.Errorf("status = %q, want under_review", d.Status())
		}
		if len(d.Evidence()) != 1 {
			t.Errorf("evidence not appended: %+v", d.Evidence())
		}
		if d.ResponseNote() != "aquí está la prueba" {
			t.Errorf("note = %q", d.ResponseNote())
		}
		if d.RespondedAt() == nil || !d.RespondedAt().Equal(now) {
			t.Errorf("respondedAt = %v, want %v", d.RespondedAt(), now)
		}
		// Contest no emite eventos de dominio.
		if len(d.PullEvents()) != 0 {
			t.Error("Contest should not emit events")
		}
	})

	t.Run("cannot contest when not open", func(t *testing.T) {
		d := openDispute(t, future)
		_ = d.Accept("", time.Now())
		d.PullEvents()
		err := d.Contest(someEvidence(t), "", time.Now())
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("expired deadline rejected", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		d := overdueDispute(t, past)
		err := d.Contest(someEvidence(t), "", time.Now())
		if !errors.Is(err, ErrDisputeExpired) {
			t.Fatalf("expected ErrDisputeExpired, got %v", err)
		}
	})

	t.Run("evidence required", func(t *testing.T) {
		d := openDispute(t, future)
		err := d.Contest(nil, "sin pruebas", time.Now())
		if !errors.Is(err, ErrEvidenceRequired) {
			t.Fatalf("expected ErrEvidenceRequired, got %v", err)
		}
		if d.Status() != StatusOpen {
			t.Errorf("status should stay open, got %q", d.Status())
		}
	})
}

func TestDispute_Accept(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)

	t.Run("valid accept resolves as accepted", func(t *testing.T) {
		d := openDispute(t, future)
		if err := d.Accept("acepto", time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusAccepted || d.ResolvedNote() != "acepto" {
			t.Errorf("status/note mismatch: %q / %q", d.Status(), d.ResolvedNote())
		}
		if d.ResolvedAt() == nil {
			t.Error("resolvedAt should be set")
		}
		events := d.PullEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		resolved, ok := events[0].(DisputeResolvedEvent)
		if !ok || resolved.Outcome != "accepted" {
			t.Errorf("expected accepted DisputeResolvedEvent, got %+v", events[0])
		}
	})

	t.Run("cannot accept when not open", func(t *testing.T) {
		d := openDispute(t, future)
		_ = d.Contest(someEvidence(t), "", time.Now())
		err := d.Accept("", time.Now())
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

func TestDispute_Resolve(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)

	underReview := func(t *testing.T) *Dispute {
		d := openDispute(t, future)
		if err := d.Contest(someEvidence(t), "", time.Now()); err != nil {
			t.Fatalf("contest: %v", err)
		}
		d.PullEvents()
		return d
	}

	t.Run("won", func(t *testing.T) {
		d := underReview(t)
		if err := d.Resolve(OutcomeWon, "ganada", time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusWon {
			t.Errorf("status = %q, want won", d.Status())
		}
		ev := d.PullEvents()
		if len(ev) != 1 || ev[0].(DisputeResolvedEvent).Outcome != "won" {
			t.Errorf("expected won event, got %+v", ev)
		}
	})

	t.Run("lost", func(t *testing.T) {
		d := underReview(t)
		if err := d.Resolve(OutcomeLost, "perdida", time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusLost {
			t.Errorf("status = %q, want lost", d.Status())
		}
	})

	t.Run("cannot resolve from open", func(t *testing.T) {
		d := openDispute(t, future)
		err := d.Resolve(OutcomeWon, "", time.Now())
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("invalid outcome for resolve", func(t *testing.T) {
		for _, o := range []ResolutionOutcome{OutcomeAccepted, OutcomeExpired} {
			d := underReview(t)
			err := d.Resolve(o, "", time.Now())
			if !errors.Is(err, ErrInvalidResolutionOutcome) {
				t.Errorf("outcome %q: expected ErrInvalidResolutionOutcome, got %v", o, err)
			}
			if d.Status() != StatusUnderReview {
				t.Errorf("status should stay under_review after invalid resolve")
			}
		}
	})
}

func TestDispute_Expire(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)

	t.Run("valid expire from open", func(t *testing.T) {
		d := openDispute(t, future)
		if err := d.Expire(time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status() != StatusExpired || d.ResolvedAt() == nil {
			t.Errorf("status/resolvedAt mismatch: %q", d.Status())
		}
		ev := d.PullEvents()
		if len(ev) != 1 || ev[0].(DisputeResolvedEvent).Outcome != "expired" {
			t.Errorf("expected expired event, got %+v", ev)
		}
	})

	t.Run("cannot expire when not open", func(t *testing.T) {
		d := openDispute(t, future)
		_ = d.Accept("", time.Now())
		err := d.Expire(time.Now())
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})
}

func TestDispute_IsOverdue(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	t.Run("open past deadline is overdue", func(t *testing.T) {
		d := overdueDispute(t, past)
		if !d.IsOverdue(time.Now()) {
			t.Error("expected overdue")
		}
	})

	t.Run("open before deadline is not overdue", func(t *testing.T) {
		d := openDispute(t, future)
		if d.IsOverdue(time.Now()) {
			t.Error("should not be overdue before deadline")
		}
	})

	t.Run("resolved dispute is never overdue", func(t *testing.T) {
		d := overdueDispute(t, past)
		_ = d.Accept("", time.Now())
		if d.IsOverdue(time.Now()) {
			t.Error("resolved dispute should not be overdue")
		}
	})
}

func TestReconstituteDispute(t *testing.T) {
	openedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	responded := openedAt.Add(time.Hour)
	resolved := openedAt.Add(2 * time.Hour)
	deadline := openedAt.Add(7 * 24 * time.Hour)
	id := NewDisputeID()
	tid := TenantID("tenant-1")
	ev := []Evidence{ReconstituteEvidence(NewEvidenceID(), "receipt", "ref", "d", responded)}

	d := ReconstituteDispute(id, tid, "pay-1", "psp-1", 5000, "ARS",
		ReasonFraudulent, StatusWon, ev, "respondió", "cerró",
		deadline, openedAt, &responded, &resolved, 3)

	if d.ID() != id || d.Status() != StatusWon || d.Reason() != ReasonFraudulent {
		t.Errorf("fields not restored: %+v", d)
	}
	if len(d.Evidence()) != 1 || d.ResponseNote() != "respondió" || d.ResolvedNote() != "cerró" {
		t.Errorf("evidence/notes not restored")
	}
	if d.RespondedAt() == nil || !d.RespondedAt().Equal(responded) {
		t.Errorf("respondedAt not restored")
	}
	if d.ResolvedAt() == nil || !d.ResolvedAt().Equal(resolved) {
		t.Errorf("resolvedAt not restored")
	}
	if len(d.PullEvents()) != 0 {
		t.Error("reconstitute must not emit events")
	}
}
