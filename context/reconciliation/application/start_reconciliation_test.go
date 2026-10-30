package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

func TestStartReconciliationUseCase_Execute(t *testing.T) {
	ctx := context.Background()
	validCmd := StartReconciliationCmd{
		TenantID:   tenantA,
		Type:       "payment",
		PeriodFrom: periodFrom,
		PeriodTo:   periodTo,
	}

	t.Run("creates pending run for tenant", func(t *testing.T) {
		repo := newRunRepo()

		res, err := NewStartReconciliationUseCase(repo, clock).Execute(ctx, validCmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.saved) != 1 {
			t.Fatalf("expected 1 saved run, got %d", len(repo.saved))
		}
		run := repo.saved[0]
		if run.ID().String() != res.RunID {
			t.Errorf("result id %q != saved id %q", res.RunID, run.ID())
		}
		if run.Status() != domain.RunStatusPending || run.Type() != domain.TypePayment {
			t.Errorf("status=%q type=%q", run.Status(), run.Type())
		}
		if run.TenantID() != domain.TenantID(tenantA) {
			t.Errorf("tenant = %q", run.TenantID())
		}
		if !run.PeriodFrom().Equal(periodFrom) || !run.PeriodTo().Equal(periodTo) {
			t.Errorf("period = %v..%v", run.PeriodFrom(), run.PeriodTo())
		}
	})

	t.Run("empty tenant creates global run", func(t *testing.T) {
		repo := newRunRepo()
		cmd := validCmd
		cmd.TenantID = ""
		cmd.Type = "internal_ledger"

		if _, err := NewStartReconciliationUseCase(repo, clock).Execute(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		run := repo.saved[0]
		if run.TenantID() != "" || run.Type() != domain.TypeInternal {
			t.Errorf("tenant=%q type=%q, want global internal_ledger", run.TenantID(), run.Type())
		}
	})

	validation := []struct {
		name    string
		mutate  func(*StartReconciliationCmd)
		wantErr error
		wantMsg string
	}{
		{"invalid type", func(c *StartReconciliationCmd) { c.Type = "bank" }, nil, "invalid reconciliation type"},
		{"missing period_from", func(c *StartReconciliationCmd) { c.PeriodFrom = time.Time{} }, nil, "period_from and period_to are required"},
		{"missing period_to", func(c *StartReconciliationCmd) { c.PeriodTo = time.Time{} }, nil, "period_from and period_to are required"},
		{"invalid tenant", func(c *StartReconciliationCmd) { c.TenantID = "nope" }, nil, "invalid tenant id"},
		{"inverted period", func(c *StartReconciliationCmd) { c.PeriodFrom, c.PeriodTo = periodTo, periodFrom }, domain.ErrInvalidPeriod, ""},
		{"empty period", func(c *StartReconciliationCmd) { c.PeriodTo = c.PeriodFrom }, domain.ErrInvalidPeriod, ""},
	}
	for _, tc := range validation {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			repo := newRunRepo()
			cmd := validCmd
			tc.mutate(&cmd)

			_, err := NewStartReconciliationUseCase(repo, clock).Execute(ctx, cmd)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			if tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
			if len(repo.saved) != 0 {
				t.Error("nothing should be saved on validation error")
			}
		})
	}

	t.Run("save error is wrapped", func(t *testing.T) {
		repo := newRunRepo()
		repo.saveErr = errBoom

		_, err := NewStartReconciliationUseCase(repo, clock).Execute(ctx, validCmd)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save run") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
	})
}
