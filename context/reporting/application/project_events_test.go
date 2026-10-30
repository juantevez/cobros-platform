package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

func newProjector(w *fakeWriter, a *fakeAccounts) *ProjectEventsUseCase {
	return NewProjectEventsUseCase(w, a, fixedClock{testNow})
}

func TestProjectEventsUseCase_ProjectPaymentCaptured(t *testing.T) {
	ctx := context.Background()
	validCmd := PaymentCapturedCmd{
		PaymentID:     "pay-1",
		TenantID:      tenantA,
		Currency:      "ARS",
		Amount:        10_000,
		PlatformFee:   300,
		PSPFee:        150,
		PaymentMethod: "card",
	}

	t.Run("saves payment fact stamped with clock time", func(t *testing.T) {
		w := &fakeWriter{}

		if err := newProjector(w, &fakeAccounts{}).ProjectPaymentCaptured(ctx, validCmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := domain.PaymentFact{
			PaymentID:     "pay-1",
			TenantID:      tenantA,
			Currency:      "ARS",
			Amount:        10_000,
			PlatformFee:   300,
			PSPFee:        150,
			PaymentMethod: "card",
			CapturedAt:    testNow,
		}
		if len(w.payments) != 1 || w.payments[0] != want {
			t.Errorf("payments = %+v\nwant [%+v]", w.payments, want)
		}
	})

	t.Run("uses event time in UTC when present", func(t *testing.T) {
		w := &fakeWriter{}
		cmd := validCmd
		cmd.OccurredAt = time.Date(2026, 9, 30, 21, 0, 0, 0, time.FixedZone("ART", -3*3600))

		if err := newProjector(w, &fakeAccounts{}).ProjectPaymentCaptured(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if got := w.payments[0].CapturedAt; !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("CapturedAt = %v, want %v (not clock %v)", got, want, testNow)
		}
	})

	invalid := []struct {
		name   string
		mutate func(*PaymentCapturedCmd)
	}{
		{"missing payment id", func(c *PaymentCapturedCmd) { c.PaymentID = "" }},
		{"missing tenant", func(c *PaymentCapturedCmd) { c.TenantID = "" }},
	}
	for _, tc := range invalid {
		t.Run(tc.name+" returns ErrInvalidFact", func(t *testing.T) {
			w := &fakeWriter{}
			cmd := validCmd
			tc.mutate(&cmd)

			if err := newProjector(w, &fakeAccounts{}).ProjectPaymentCaptured(ctx, cmd); !errors.Is(err, domain.ErrInvalidFact) {
				t.Fatalf("expected ErrInvalidFact, got %v", err)
			}
			if len(w.payments) != 0 {
				t.Error("nothing should be saved")
			}
		})
	}

	t.Run("writer error is returned", func(t *testing.T) {
		w := &fakeWriter{paymentErr: errBoom}
		if err := newProjector(w, &fakeAccounts{}).ProjectPaymentCaptured(ctx, validCmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestProjectEventsUseCase_ProjectEntryPosted(t *testing.T) {
	ctx := context.Background()
	accounts := func() *fakeAccounts {
		return &fakeAccounts{types: map[string]string{
			"acc-merchant": "merchant_balance",
			"acc-transit":  "in_transit",
		}}
	}
	validCmd := EntryPostedCmd{
		EntryID:  "entry-1",
		TenantID: tenantA,
		Postings: []PostingCmd{
			{AccountID: "acc-transit", Direction: "debit", Amount: 10_000, Currency: "ARS"},
			{AccountID: "acc-merchant", Direction: "credit", Amount: 10_000, Currency: "ARS"},
		},
	}

	t.Run("saves one movement per posting with denormalized account type", func(t *testing.T) {
		w := &fakeWriter{}

		if err := newProjector(w, accounts()).ProjectEntryPosted(ctx, validCmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.LedgerMovement{
			{EntryID: "entry-1", AccountID: "acc-transit", Direction: "debit", TenantID: tenantA,
				AccountType: "in_transit", Currency: "ARS", Amount: 10_000, PostedAt: testNow},
			{EntryID: "entry-1", AccountID: "acc-merchant", Direction: "credit", TenantID: tenantA,
				AccountType: "merchant_balance", Currency: "ARS", Amount: 10_000, PostedAt: testNow},
		}
		if !reflect.DeepEqual(w.movements, want) {
			t.Errorf("movements = %+v\nwant %+v", w.movements, want)
		}
	})

	t.Run("uses event time for every movement when present", func(t *testing.T) {
		w := &fakeWriter{}
		cmd := validCmd
		cmd.OccurredAt = time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

		if err := newProjector(w, accounts()).ProjectEntryPosted(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, m := range w.movements {
			if !m.PostedAt.Equal(cmd.OccurredAt) {
				t.Errorf("PostedAt = %v, want %v", m.PostedAt, cmd.OccurredAt)
			}
		}
	})

	t.Run("unknown account is projected with empty type", func(t *testing.T) {
		w := &fakeWriter{}
		cmd := EntryPostedCmd{EntryID: "e", TenantID: tenantA, Postings: []PostingCmd{
			{AccountID: "acc-unknown", Direction: "debit", Amount: 1, Currency: "ARS"},
		}}

		if err := newProjector(w, accounts()).ProjectEntryPosted(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.movements) != 1 || w.movements[0].AccountType != "" {
			t.Errorf("movements = %+v", w.movements)
		}
	})

	t.Run("postings without account are skipped", func(t *testing.T) {
		w := &fakeWriter{}
		a := accounts()
		cmd := EntryPostedCmd{EntryID: "e", TenantID: tenantA, Postings: []PostingCmd{
			{AccountID: "", Direction: "debit", Amount: 1, Currency: "ARS"},
			{AccountID: "acc-merchant", Direction: "credit", Amount: 1, Currency: "ARS"},
		}}

		if err := newProjector(w, a).ProjectEntryPosted(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.movements) != 1 || w.movements[0].AccountID != "acc-merchant" {
			t.Errorf("movements = %+v", w.movements)
		}
		if !reflect.DeepEqual(a.lookups, []string{tenantA + "/acc-merchant"}) {
			t.Errorf("lookups = %v, want only acc-merchant, with the event tenant", a.lookups)
		}
	})

	t.Run("entry without postings saves nothing", func(t *testing.T) {
		w := &fakeWriter{}
		if err := newProjector(w, accounts()).ProjectEntryPosted(ctx, EntryPostedCmd{EntryID: "e", TenantID: tenantA}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.movementCalls != 0 {
			t.Error("nothing should be saved")
		}
	})

	invalid := []struct {
		name   string
		mutate func(*EntryPostedCmd)
	}{
		{"missing entry id", func(c *EntryPostedCmd) { c.EntryID = "" }},
		{"missing tenant", func(c *EntryPostedCmd) { c.TenantID = "" }},
	}
	for _, tc := range invalid {
		t.Run(tc.name+" returns ErrInvalidFact", func(t *testing.T) {
			w := &fakeWriter{}
			a := accounts()
			cmd := validCmd
			tc.mutate(&cmd)

			if err := newProjector(w, a).ProjectEntryPosted(ctx, cmd); !errors.Is(err, domain.ErrInvalidFact) {
				t.Fatalf("expected ErrInvalidFact, got %v", err)
			}
			if w.movementCalls != 0 || len(a.lookups) != 0 {
				t.Error("nothing should be looked up or saved")
			}
		})
	}

	t.Run("account lookup error aborts projection", func(t *testing.T) {
		w := &fakeWriter{}
		a := accounts()
		a.err = errBoom

		if err := newProjector(w, a).ProjectEntryPosted(ctx, validCmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if w.movementCalls != 0 {
			t.Error("nothing should be saved")
		}
	})

	t.Run("writer error stops at failing posting", func(t *testing.T) {
		// Los postings previos quedan guardados; la re-entrega del evento los
		// re-proyecta sin duplicar gracias a la idempotencia del writer.
		w := &fakeWriter{movementErr: errBoom, errOnMovement: 2}
		cmd := validCmd
		cmd.Postings = append(cmd.Postings, PostingCmd{AccountID: "acc-merchant", Direction: "debit", Amount: 5, Currency: "ARS"})

		if err := newProjector(w, accounts()).ProjectEntryPosted(ctx, cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if w.movementCalls != 2 || len(w.movements) != 1 {
			t.Errorf("calls=%d saved=%d, want 2/1", w.movementCalls, len(w.movements))
		}
	})
}

func TestProjectPaymentRefunded(t *testing.T) {
	ctx := context.Background()
	occurred := time.Date(2026, 9, 20, 15, 0, 0, 0, time.FixedZone("ART", -3*3600))

	t.Run("projects the refund keyed by payment", func(t *testing.T) {
		w := &fakeWriter{}
		err := newProjector(w, &fakeAccounts{}).ProjectPaymentRefunded(ctx, PaymentRefundedCmd{
			PaymentID: "pay-1", TenantID: tenantA, Currency: "ARS", Amount: 1000, OccurredAt: occurred,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := domain.PaymentAdjustment{Kind: domain.AdjustmentRefund, SourceID: "pay-1", PaymentID: "pay-1",
			TenantID: tenantA, Currency: "ARS", Amount: 1000, OccurredAt: occurred.UTC()}
		if len(w.adjustments) != 1 || w.adjustments[0] != want {
			t.Errorf("adjustments = %+v\nwant %+v", w.adjustments, want)
		}
	})

	t.Run("without event time uses the clock", func(t *testing.T) {
		w := &fakeWriter{}
		_ = newProjector(w, &fakeAccounts{}).ProjectPaymentRefunded(ctx, PaymentRefundedCmd{PaymentID: "p", TenantID: tenantA, Amount: 1})
		if !w.adjustments[0].OccurredAt.Equal(testNow) {
			t.Errorf("OccurredAt = %v, want clock", w.adjustments[0].OccurredAt)
		}
	})

	for name, cmd := range map[string]PaymentRefundedCmd{
		"missing payment": {TenantID: tenantA, Amount: 1},
		"missing tenant":  {PaymentID: "p", Amount: 1},
	} {
		t.Run(name+" is an invalid fact", func(t *testing.T) {
			w := &fakeWriter{}
			if err := newProjector(w, &fakeAccounts{}).ProjectPaymentRefunded(ctx, cmd); !errors.Is(err, domain.ErrInvalidFact) {
				t.Fatalf("expected ErrInvalidFact, got %v", err)
			}
			if len(w.adjustments) != 0 {
				t.Error("nothing should be saved")
			}
		})
	}

	t.Run("writer error is returned", func(t *testing.T) {
		w := &fakeWriter{adjustmentErr: errBoom}
		if err := newProjector(w, &fakeAccounts{}).ProjectPaymentRefunded(ctx, PaymentRefundedCmd{PaymentID: "p", TenantID: tenantA}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestProjectDisputeResolved(t *testing.T) {
	ctx := context.Background()
	base := DisputeResolvedCmd{DisputeID: "dis-1", PaymentID: "pay-1", TenantID: tenantA, Currency: "ARS", Amount: 700, OccurredAt: testNow}

	for _, outcome := range []string{"lost", "accepted", "expired"} {
		t.Run(outcome+" dispute is a chargeback keyed by dispute", func(t *testing.T) {
			w := &fakeWriter{}
			cmd := base
			cmd.Outcome = outcome
			if err := newProjector(w, &fakeAccounts{}).ProjectDisputeResolved(ctx, cmd); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := domain.PaymentAdjustment{Kind: domain.AdjustmentChargeback, SourceID: "dis-1", PaymentID: "pay-1",
				TenantID: tenantA, Currency: "ARS", Amount: 700, OccurredAt: testNow}
			if len(w.adjustments) != 1 || w.adjustments[0] != want {
				t.Errorf("adjustments = %+v\nwant %+v", w.adjustments, want)
			}
		})
	}

	t.Run("won dispute moves no money", func(t *testing.T) {
		w := &fakeWriter{adjustmentErr: errBoom}
		cmd := base
		cmd.Outcome = "won"
		if err := newProjector(w, &fakeAccounts{}).ProjectDisputeResolved(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown outcome is an invalid fact", func(t *testing.T) {
		w := &fakeWriter{}
		cmd := base
		cmd.Outcome = "pending"
		err := newProjector(w, &fakeAccounts{}).ProjectDisputeResolved(ctx, cmd)
		if !errors.Is(err, domain.ErrInvalidFact) || !strings.Contains(err.Error(), `"pending"`) {
			t.Fatalf("expected ErrInvalidFact naming the outcome, got %v", err)
		}
		if len(w.adjustments) != 0 {
			t.Error("nothing should be saved")
		}
	})

	for name, mutate := range map[string]func(*DisputeResolvedCmd){
		"missing dispute": func(c *DisputeResolvedCmd) { c.DisputeID = "" },
		"missing payment": func(c *DisputeResolvedCmd) { c.PaymentID = "" },
		"missing tenant":  func(c *DisputeResolvedCmd) { c.TenantID = "" },
	} {
		t.Run(name+" is an invalid fact", func(t *testing.T) {
			cmd := base
			cmd.Outcome = "lost"
			mutate(&cmd)
			if err := newProjector(&fakeWriter{}, &fakeAccounts{}).ProjectDisputeResolved(ctx, cmd); !errors.Is(err, domain.ErrInvalidFact) {
				t.Fatalf("expected ErrInvalidFact, got %v", err)
			}
		})
	}
}
