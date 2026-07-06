package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

func TestMonitorTransactionUseCase_Execute(t *testing.T) {
	rules := MonitoringRules{
		ThresholdAmount: 1_000_000,
		VelocityCount:   10,
		VelocityWindow:  10 * time.Minute,
	}

	baseCmd := func() MonitorTransactionCmd {
		return MonitorTransactionCmd{
			TenantID:      validTenant(),
			PaymentID:     "pay-1",
			Amount:        500_000,
			Currency:      "ARS",
			PaymentMethod: "card",
		}
	}

	newUC := func(r *fakeAlertRepo, tr *fakeTxReader, p *fakePublisher) *MonitorTransactionUseCase {
		return NewMonitorTransactionUseCase(r, tr, fakeTx{}, p, newClock(), rules)
	}

	t.Run("below both thresholds: no alert", func(t *testing.T) {
		r := newAlertRepo()
		tr := &fakeTxReader{count: 3}
		uc := newUC(r, tr, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 0 {
			t.Errorf("expected no alerts, got %d", len(r.saved))
		}
	})

	t.Run("amount over threshold raises threshold alert", func(t *testing.T) {
		r := newAlertRepo()
		tr := &fakeTxReader{count: 0}
		p := &fakePublisher{}
		uc := newUC(r, tr, p)
		cmd := baseCmd()
		cmd.Amount = 1_000_000
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(r.saved))
		}
		a := r.saved[0]
		if a.Type() != domain.AlertTransactionThreshold || a.RiskLevel() != domain.RiskMedium {
			t.Errorf("type/risk mismatch: %q / %q", a.Type(), a.RiskLevel())
		}
		if a.Subject() != "pay-1" {
			t.Errorf("subject = %q", a.Subject())
		}
		if a.Details()["amount"] != "1000000" || a.Details()["threshold"] != "1000000" {
			t.Errorf("details mismatch: %+v", a.Details())
		}
	})

	t.Run("velocity over count raises velocity alert", func(t *testing.T) {
		r := newAlertRepo()
		tr := &fakeTxReader{count: 10}
		uc := newUC(r, tr, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(r.saved))
		}
		a := r.saved[0]
		if a.Type() != domain.AlertTransactionVelocity || a.RiskLevel() != domain.RiskHigh {
			t.Errorf("type/risk mismatch: %q / %q", a.Type(), a.RiskLevel())
		}
		if a.Score() != 90 || a.Details()["count"] != "10" {
			t.Errorf("score/details mismatch: %d / %+v", a.Score(), a.Details())
		}
	})

	t.Run("both rules fire: two alerts", func(t *testing.T) {
		r := newAlertRepo()
		tr := &fakeTxReader{count: 15}
		p := &fakePublisher{}
		uc := newUC(r, tr, p)
		cmd := baseCmd()
		cmd.Amount = 2_000_000
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 2 {
			t.Fatalf("expected 2 alerts, got %d", len(r.saved))
		}
		if len(p.published) != 2 {
			t.Errorf("expected 2 published events, got %d", len(p.published))
		}
	})

	t.Run("velocity window computed from clock", func(t *testing.T) {
		r := newAlertRepo()
		tr := &fakeTxReader{count: 0}
		uc := newUC(r, tr, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantSince := testNow.Add(-10 * time.Minute)
		if !tr.gotSince.Equal(wantSince) {
			t.Errorf("since = %v, want %v", tr.gotSince, wantSince)
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakeTxReader{}, &fakePublisher{})
		cmd := baseCmd()
		cmd.TenantID = "bad"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("threshold save error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(r, &fakeTxReader{count: 0}, &fakePublisher{})
		cmd := baseCmd()
		cmd.Amount = 1_000_000
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("txReader error propagated", func(t *testing.T) {
		tr := &fakeTxReader{err: errBoom}
		uc := newUC(newAlertRepo(), tr, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("velocity save error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(r, &fakeTxReader{count: 20}, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestRiskScoreForAmount(t *testing.T) {
	cases := []struct {
		amount, threshold int64
		want              int
	}{
		{1_000_000, 1_000_000, 70},    // ratio 1 → 60+10
		{2_500_000, 1_000_000, 80},    // ratio 2 → 60+20
		{999, 1_000_000, 60},          // ratio 0 → 60
		{100_000_000, 1_000_000, 100}, // cap a 100
		{500, 0, 80},                  // threshold 0 → fallback defensivo
	}
	for _, c := range cases {
		if got := riskScoreForAmount(c.amount, c.threshold); got != c.want {
			t.Errorf("riskScoreForAmount(%d,%d) = %d, want %d", c.amount, c.threshold, got, c.want)
		}
	}
}

func TestDefaultMonitoringRules(t *testing.T) {
	r := DefaultMonitoringRules()
	if r.ThresholdAmount != 1_000_000 || r.VelocityCount != 10 || r.VelocityWindow != 10*time.Minute {
		t.Errorf("unexpected defaults: %+v", r)
	}
}
