package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewIDs(t *testing.T) {
	if _, err := uuid.Parse(NewDisputeID().String()); err != nil {
		t.Errorf("NewDisputeID not a uuid: %v", err)
	}
	if _, err := uuid.Parse(NewEvidenceID().String()); err != nil {
		t.Errorf("NewEvidenceID not a uuid: %v", err)
	}
	d1, d2 := NewDisputeID(), NewDisputeID()
	if d1 == d2 {
		t.Error("NewDisputeID should be unique")
	}
}

func TestParseDisputeID(t *testing.T) {
	u := uuid.NewString()
	id, err := ParseDisputeID(u)
	if err != nil || id.String() != u {
		t.Fatalf("valid parse failed: %q, %v", id, err)
	}
	if _, err := ParseDisputeID("nope"); err == nil {
		t.Error("expected error for invalid dispute id")
	}
}

func TestParseTenantID(t *testing.T) {
	u := uuid.NewString()
	tid, err := ParseTenantID(u)
	if err != nil || tid.String() != u {
		t.Fatalf("valid parse failed: %q, %v", tid, err)
	}
	if _, err := ParseTenantID(""); err == nil {
		t.Error("expected error for empty tenant id")
	}
}

func TestDisputeStatus_IsFinal(t *testing.T) {
	final := []DisputeStatus{StatusWon, StatusLost, StatusAccepted, StatusExpired}
	for _, s := range final {
		if !s.IsFinal() {
			t.Errorf("%q should be final", s)
		}
	}
	nonFinal := []DisputeStatus{StatusOpen, StatusUnderReview}
	for _, s := range nonFinal {
		if s.IsFinal() {
			t.Errorf("%q should not be final", s)
		}
	}
}

func TestDisputeStatus_String(t *testing.T) {
	if StatusUnderReview.String() != "under_review" {
		t.Errorf("got %q", StatusUnderReview.String())
	}
}

func TestParseDisputeReason(t *testing.T) {
	valid := []DisputeReason{
		ReasonFraudulent, ReasonProductNotReceived, ReasonProductUnacceptable,
		ReasonDuplicate, ReasonCreditNotProcessed, ReasonGeneral,
	}
	for _, want := range valid {
		got, err := ParseDisputeReason(want.String())
		if err != nil || got != want {
			t.Errorf("ParseDisputeReason(%q) = %q, %v", want, got, err)
		}
	}

	t.Run("invalid rejected", func(t *testing.T) {
		_, err := ParseDisputeReason("bogus")
		if !errors.Is(err, ErrInvalidDisputeReason) {
			t.Fatalf("expected ErrInvalidDisputeReason, got %v", err)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := ParseDisputeReason("")
		if !errors.Is(err, ErrInvalidDisputeReason) {
			t.Fatalf("expected ErrInvalidDisputeReason, got %v", err)
		}
	})
}

func TestParseResolutionOutcome(t *testing.T) {
	valid := []ResolutionOutcome{OutcomeWon, OutcomeLost, OutcomeAccepted, OutcomeExpired}
	for _, want := range valid {
		got, err := ParseResolutionOutcome(want.String())
		if err != nil || got != want {
			t.Errorf("ParseResolutionOutcome(%q) = %q, %v", want, got, err)
		}
	}

	t.Run("invalid rejected", func(t *testing.T) {
		_, err := ParseResolutionOutcome("nope")
		if !errors.Is(err, ErrInvalidResolutionOutcome) {
			t.Fatalf("expected ErrInvalidResolutionOutcome, got %v", err)
		}
	})
}

func TestNewEvidence(t *testing.T) {
	before := time.Now().UTC()
	id := NewEvidenceID()
	e := NewEvidence(id, "receipt", "s3://ref", "comprobante")
	after := time.Now().UTC()

	if e.ID() != id || e.EvidenceType() != "receipt" || e.Reference() != "s3://ref" {
		t.Errorf("fields mismatch: %+v", e)
	}
	if e.Description() != "comprobante" {
		t.Errorf("description = %q", e.Description())
	}
	if e.SubmittedAt().Before(before) || e.SubmittedAt().After(after) {
		t.Errorf("submittedAt %v not within [%v,%v]", e.SubmittedAt(), before, after)
	}
	if e.SubmittedAt().Location() != time.UTC {
		t.Errorf("submittedAt not UTC")
	}
}

func TestReconstituteEvidence(t *testing.T) {
	id := NewEvidenceID()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := ReconstituteEvidence(id, "tracking", "ref-2", "envío", at)
	if e.ID() != id || e.EvidenceType() != "tracking" || e.Reference() != "ref-2" {
		t.Errorf("fields not restored: %+v", e)
	}
	if !e.SubmittedAt().Equal(at) {
		t.Errorf("submittedAt not restored: %v", e.SubmittedAt())
	}
}
