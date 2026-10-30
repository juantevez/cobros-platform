package domain

import (
	"errors"
	"testing"
	"time"
)

func newTestDiscrepancy() *Discrepancy {
	return NewDiscrepancy(NewDiscrepancyID(), NewRunID(), testTenant, DiscrepancyAmountMismatch, "PSP-1", "100 ARS", "90 ARS")
}

func discrepancyWithStatus(st DiscrepancyStatus) *Discrepancy {
	return ReconstituteDiscrepancy(
		NewDiscrepancyID(), NewRunID(), testTenant, DiscrepancyAmountMismatch,
		"PSP-1", "100 ARS", "90 ARS", st, "", "", nil, testFrom,
	)
}

func TestNewDiscrepancy(t *testing.T) {
	id, runID := NewDiscrepancyID(), NewRunID()
	before := time.Now().UTC()

	d := NewDiscrepancy(id, runID, testTenant, DiscrepancyMissingInSystem, "PSP-9", "", `{"x":1}`)

	if d.ID() != id || d.RunID() != runID || d.TenantID() != testTenant {
		t.Error("identity not set")
	}
	if d.Type() != DiscrepancyMissingInSystem || d.RecordID() != "PSP-9" || d.SystemValue() != "" || d.ExternalValue() != `{"x":1}` {
		t.Errorf("data not set: %q %q %q %q", d.Type(), d.RecordID(), d.SystemValue(), d.ExternalValue())
	}
	if d.Status() != DiscrepancyOpen {
		t.Errorf("status = %q, want open", d.Status())
	}
	if d.Notes() != "" || d.ResolvedBy() != "" || d.ResolvedAt() != nil {
		t.Error("resolution fields should be empty")
	}
	if d.CreatedAt().Before(before) || d.CreatedAt().Location() != time.UTC {
		t.Errorf("createdAt = %v", d.CreatedAt())
	}
}

func TestDiscrepancy_ResolveAndIgnore(t *testing.T) {
	ops := []struct {
		name string
		do   func(*Discrepancy, string, string) error
		want DiscrepancyStatus
	}{
		{"resolve", (*Discrepancy).Resolve, DiscrepancyResolved},
		{"ignore", (*Discrepancy).Ignore, DiscrepancyIgnored},
	}

	for _, op := range ops {
		t.Run(op.name+" open discrepancy", func(t *testing.T) {
			d := newTestDiscrepancy()
			before := time.Now().UTC()

			if err := op.do(d, "op-1", "checked with PSP"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Status() != op.want {
				t.Errorf("status = %q, want %q", d.Status(), op.want)
			}
			if d.ResolvedBy() != "op-1" || d.Notes() != "checked with PSP" {
				t.Errorf("by=%q notes=%q", d.ResolvedBy(), d.Notes())
			}
			if d.ResolvedAt() == nil || d.ResolvedAt().Before(before) || d.ResolvedAt().Location() != time.UTC {
				t.Errorf("resolvedAt = %v", d.ResolvedAt())
			}

			evs := d.PullEvents()
			if len(evs) != 1 {
				t.Fatalf("got %d events, want 1", len(evs))
			}
			ev := evs[0].(DiscrepancyResolvedEvent)
			if ev.DiscrepancyID != d.ID().String() || ev.RunID != d.RunID().String() || ev.TenantID != d.TenantID().String() ||
				ev.EventTenantID() != d.TenantID().String() || ev.Type != d.Type().String() || ev.Status != op.want.String() || ev.ResolvedBy != "op-1" {
				t.Errorf("event = %+v", ev)
			}
			if len(d.PullEvents()) != 0 {
				t.Error("PullEvents must clear the events")
			}
		})

		for _, st := range []DiscrepancyStatus{DiscrepancyResolved, DiscrepancyIgnored} {
			t.Run(op.name+" rejected from "+st.String(), func(t *testing.T) {
				d := discrepancyWithStatus(st)
				if err := op.do(d, "op-2", "again"); !errors.Is(err, ErrDiscrepancyAlreadyResolved) {
					t.Fatalf("expected ErrDiscrepancyAlreadyResolved, got %v", err)
				}
				if d.Status() != st || d.ResolvedBy() != "" || d.Notes() != "" || d.ResolvedAt() != nil || len(d.PullEvents()) != 0 {
					t.Error("rejected operation must not change the discrepancy")
				}
			})
		}
	}
}

func TestReconstituteDiscrepancy(t *testing.T) {
	id, runID := NewDiscrepancyID(), NewRunID()
	resolved := testTo

	d := ReconstituteDiscrepancy(
		id, runID, testTenant, DiscrepancyLedgerImbalance,
		"global", "0", "5", DiscrepancyResolved, "fixed entry", "op-1", &resolved, testFrom,
	)

	if d.ID() != id || d.RunID() != runID || d.TenantID() != testTenant || d.Type() != DiscrepancyLedgerImbalance {
		t.Error("identity not restored")
	}
	if d.RecordID() != "global" || d.SystemValue() != "0" || d.ExternalValue() != "5" {
		t.Error("values not restored")
	}
	if d.Status() != DiscrepancyResolved || d.Notes() != "fixed entry" || d.ResolvedBy() != "op-1" || d.ResolvedAt() != &resolved {
		t.Error("resolution not restored")
	}
	if !d.CreatedAt().Equal(testFrom) {
		t.Errorf("createdAt = %v", d.CreatedAt())
	}
}
