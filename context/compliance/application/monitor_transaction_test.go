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
		VolumeAmount:    5_000_000,
	}
	wantWindowStart := testNow.Add(-10 * time.Minute).Format(time.RFC3339)

	baseCmd := func() MonitorTransactionCmd {
		return MonitorTransactionCmd{
			TenantID:      validTenant(),
			PaymentID:     "pay-1",
			Amount:        500_000,
			Currency:      "ARS",
			PaymentMethod: "card",
		}
	}

	newUCWith := func(r *fakeAlertRepo, tl *fakeTxLog, p *fakePublisher, rules MonitoringRules) *MonitorTransactionUseCase {
		return NewMonitorTransactionUseCase(r, tl, fakeTx{}, p, newClock(), rules)
	}
	newUC := func(r *fakeAlertRepo, tl *fakeTxLog, p *fakePublisher) *MonitorTransactionUseCase {
		return newUCWith(r, tl, p, rules)
	}

	t.Run("records payment in own projection", func(t *testing.T) {
		tl := &fakeTxLog{}
		uc := newUC(newAlertRepo(), tl, &fakePublisher{})
		cmd := baseCmd()
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tl.recorded) != 1 {
			t.Fatalf("expected 1 recorded payment, got %d", len(tl.recorded))
		}
		got := tl.recorded[0]
		if got.PaymentID != "pay-1" || got.TenantID != cmd.TenantID || got.Amount != 500_000 ||
			got.Currency != "ARS" || !got.CapturedAt.Equal(testNow) {
			t.Errorf("recorded payment mismatch: %+v", got)
		}
	})

	t.Run("below all thresholds: no alert", func(t *testing.T) {
		r := newAlertRepo()
		uc := newUC(r, &fakeTxLog{count: 3, sum: 1_000_000}, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 0 {
			t.Errorf("expected no alerts, got %d", len(r.saved))
		}
	})

	t.Run("amount over threshold raises threshold alert", func(t *testing.T) {
		r := newAlertRepo()
		p := &fakePublisher{}
		uc := newUC(r, &fakeTxLog{}, p)
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

	t.Run("velocity alert uses cooldown keyed by rule, subject by payment", func(t *testing.T) {
		r := newAlertRepo()
		uc := newUC(r, &fakeTxLog{count: 10}, &fakePublisher{})
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
		if a.Subject() != "velocity:pay-1" {
			t.Errorf("subject = %q, want velocity:pay-1", a.Subject())
		}
		if len(r.gotPrefix) != 1 || r.gotPrefix[0] != "velocity:" || !r.gotCooldownSince.Equal(testNow.Add(-10*time.Minute)) {
			t.Errorf("cooldown = %v since %v", r.gotPrefix, r.gotCooldownSince)
		}
		if a.Score() != 90 || a.Details()["count"] != "10" || a.Details()["payment_id"] != "pay-1" ||
			a.Details()["window_start"] != wantWindowStart {
			t.Errorf("score/details mismatch: %d / %+v", a.Score(), a.Details())
		}
	})

	t.Run("recent alert of the same rule is a no-op (cooldown)", func(t *testing.T) {
		r := newAlertRepo()
		r.recent = true
		p := &fakePublisher{}
		uc := newUC(r, &fakeTxLog{count: 50, sum: 99_000_000}, p)
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 0 || len(p.published) != 0 {
			t.Errorf("cooldown should suppress window alerts: saved=%d published=%d", len(r.saved), len(p.published))
		}
	})

	t.Run("volume over amount raises volume alert per currency", func(t *testing.T) {
		r := newAlertRepo()
		tl := &fakeTxLog{count: 2, sum: 5_000_000}
		uc := newUC(r, tl, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tl.gotCurrency != "ARS" {
			t.Errorf("sum currency = %q, want ARS", tl.gotCurrency)
		}
		if len(r.saved) != 1 {
			t.Fatalf("expected 1 alert, got %d", len(r.saved))
		}
		a := r.saved[0]
		if a.Type() != domain.AlertTransactionVolume || a.RiskLevel() != domain.RiskHigh {
			t.Errorf("type/risk mismatch: %q / %q", a.Type(), a.RiskLevel())
		}
		if a.Subject() != "volume:ARS:pay-1" || r.gotPrefix[0] != "volume:ARS:" {
			t.Errorf("subject/prefix = %q / %v", a.Subject(), r.gotPrefix)
		}
		if a.Score() != 70 || a.Details()["total_amount"] != "5000000" || a.Details()["threshold"] != "5000000" {
			t.Errorf("score/details mismatch: %d / %+v", a.Score(), a.Details())
		}
	})

	t.Run("event time drives the window and the alert timestamps", func(t *testing.T) {
		r := newAlertRepo()
		tl := &fakeTxLog{count: 10}
		uc := newUC(r, tl, &fakePublisher{})
		captured := testNow.Add(-3 * time.Hour) // backlog: evento de hace 3 h
		cmd := baseCmd()
		cmd.CapturedAt = captured
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !tl.recorded[0].CapturedAt.Equal(captured) {
			t.Errorf("captured_at = %v, want event time %v", tl.recorded[0].CapturedAt, captured)
		}
		if !tl.gotUntil.Equal(captured) || !tl.gotSince.Equal(captured.Add(-10*time.Minute)) {
			t.Errorf("window = (%v, %v], want ending at event time", tl.gotSince, tl.gotUntil)
		}
		if !r.saved[0].CreatedAt().Equal(captured) {
			t.Errorf("alert createdAt = %v, want %v", r.saved[0].CreatedAt(), captured)
		}
	})

	t.Run("volume rule disabled when VolumeAmount is zero", func(t *testing.T) {
		r := newAlertRepo()
		tl := &fakeTxLog{sum: 99_000_000}
		noVolume := rules
		noVolume.VolumeAmount = 0
		uc := newUCWith(r, tl, &fakePublisher{}, noVolume)
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tl.sumCalls != 0 || len(r.saved) != 0 {
			t.Errorf("volume rule should be skipped: sumCalls=%d saved=%d", tl.sumCalls, len(r.saved))
		}
	})

	t.Run("window rules disabled when VelocityWindow is zero", func(t *testing.T) {
		r := newAlertRepo()
		tl := &fakeTxLog{count: 100, sum: 99_000_000}
		noWindow := rules
		noWindow.VelocityWindow = 0
		uc := newUCWith(r, tl, &fakePublisher{}, noWindow)
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 0 {
			t.Errorf("expected no window alerts, got %d", len(r.saved))
		}
	})

	t.Run("all rules fire: three alerts", func(t *testing.T) {
		r := newAlertRepo()
		p := &fakePublisher{}
		uc := newUC(r, &fakeTxLog{count: 15, sum: 20_000_000}, p)
		cmd := baseCmd()
		cmd.Amount = 2_000_000
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.saved) != 3 {
			t.Fatalf("expected 3 alerts, got %d", len(r.saved))
		}
		if len(p.published) != 3 {
			t.Errorf("expected 3 published events, got %d", len(p.published))
		}
	})

	t.Run("velocity window computed from clock", func(t *testing.T) {
		tl := &fakeTxLog{}
		uc := newUC(newAlertRepo(), tl, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantSince := testNow.Add(-10 * time.Minute)
		if !tl.gotSince.Equal(wantSince) || !tl.gotUntil.Equal(testNow) {
			t.Errorf("window = (%v, %v], want (%v, %v]", tl.gotSince, tl.gotUntil, wantSince, testNow)
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakeTxLog{}, &fakePublisher{})
		cmd := baseCmd()
		cmd.TenantID = "bad"
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("record error propagated before evaluating rules", func(t *testing.T) {
		r := newAlertRepo()
		uc := newUC(r, &fakeTxLog{recordErr: errBoom}, &fakePublisher{})
		cmd := baseCmd()
		cmd.Amount = 1_000_000
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if len(r.saved) != 0 {
			t.Errorf("no alert expected when projection fails, got %d", len(r.saved))
		}
	})

	t.Run("threshold save error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(r, &fakeTxLog{}, &fakePublisher{})
		cmd := baseCmd()
		cmd.Amount = 1_000_000
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("count error propagated", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakeTxLog{countErr: errBoom}, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("sum error propagated", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakeTxLog{sumErr: errBoom}, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("velocity save error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(r, &fakeTxLog{count: 20}, &fakePublisher{})
		if err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("volume save error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.saveErr = errBoom
		uc := newUC(r, &fakeTxLog{sum: 6_000_000}, &fakePublisher{})
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
	if r.ThresholdAmount != 1_000_000 || r.VelocityCount != 10 || r.VelocityWindow != 10*time.Minute ||
		r.VolumeAmount != 5_000_000 {
		t.Errorf("unexpected defaults: %+v", r)
	}
}
