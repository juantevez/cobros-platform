package application

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var recoverNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// stuckPayment es un pago en processing con la comisión de la plataforma ya
// fijada en el claim, sin cambios desde stuckSince.
func stuckPayment(stuckSince time.Time) *domain.Payment {
	return domain.ReconstitutePayment(
		domain.NewPaymentID(), domain.TenantID(tenantA),
		"order-9", "",
		domain.ReconstituteMoney(10_000, "ARS"),
		domain.ReconstituteMoney(300, "ARS"),
		domain.ReconstituteMoney(0, "ARS"),
		testPayer(), domain.MethodCard,
		"", "",
		domain.StatusProcessing, "", nil,
		nil, nil, nil,
		stuckSince.Add(-time.Second), stuckSince, "", nil,
	)
}

type recoverDeps struct {
	repo *fakeRepo
	psp  *fakePSP
	pub  *fakePublisher
}

func newRecoverDeps(p ...*domain.Payment) *recoverDeps {
	return &recoverDeps{repo: &fakeRepo{stuck: p}, psp: &fakePSP{}, pub: &fakePublisher{}}
}

func (d *recoverDeps) run(t *testing.T) string {
	t.Helper()
	var logs strings.Builder
	NewProcessingRecoverer(d.repo, &fakeRouter{psp: d.psp}, &fakeTx{}, d.pub, fixedClock{recoverNow},
		slog.New(slog.NewTextHandler(&logs, nil))).runCycle(context.Background())
	return logs.String()
}

// Los pagos que quedaron en processing (resultado desconocido, captura sin
// persistir, proceso caído) se resuelven preguntándole al PSP.
func TestProcessingRecoverer(t *testing.T) {
	t.Run("asks only for payments stuck longer than stuckAfter", func(t *testing.T) {
		d := newRecoverDeps()
		d.run(t)
		if want := recoverNow.Add(-stuckAfter); !d.repo.stuckBefore.Equal(want) {
			t.Errorf("olderThan = %v, want %v", d.repo.stuckBefore, want)
		}
	})

	t.Run("captured at the PSP: records the capture with the claimed fee", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-5 * time.Minute))
		d := newRecoverDeps(p)
		d.psp.lookup = PSPCaptureLookup{Found: true, Approved: true, PSPReference: "PSP-77", PSPFee: 150}

		d.run(t)

		if len(d.psp.lookups) != 1 || d.psp.lookups[0].IdempotencyKey != "order-9" || d.psp.lookups[0].Amount != 10_000 {
			t.Fatalf("lookups = %+v", d.psp.lookups)
		}
		if p.Status() != domain.StatusCaptured || p.PSPReference() != "PSP-77" || p.PlatformFee().Amount() != 300 || p.PSPFee().Amount() != 150 {
			t.Errorf("payment = %q ref=%q fee=%d psp_fee=%d", p.Status(), p.PSPReference(), p.PlatformFee().Amount(), p.PSPFee().Amount())
		}
		if !reflect.DeepEqual(d.repo.froms, []domain.PaymentStatus{domain.StatusProcessing}) {
			t.Errorf("update froms = %v, want [processing]", d.repo.froms)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payment.captured.v1"}) {
			t.Errorf("events = %v", got)
		}
	})

	t.Run("declined at the PSP: fails the payment", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-5 * time.Minute))
		d := newRecoverDeps(p)
		d.psp.lookup = PSPCaptureLookup{Found: true, Approved: false}
		d.run(t)
		if p.Status() != domain.StatusFailed || !reflect.DeepEqual(d.pub.eventTypes(), []string{"payment.failed.v1"}) {
			t.Errorf("status = %q events = %v", p.Status(), d.pub.eventTypes())
		}
	})

	t.Run("no record at the PSP yet: waits", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-5 * time.Minute))
		d := newRecoverDeps(p)
		d.run(t)
		if p.Status() != domain.StatusProcessing || len(d.repo.updates) != 0 {
			t.Errorf("status = %q updates = %v, want untouched", p.Status(), d.repo.updates)
		}
	})

	t.Run("no record at the PSP after notFoundAfter: fails the payment", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-notFoundAfter))
		d := newRecoverDeps(p)
		d.run(t)
		if p.Status() != domain.StatusFailed || !strings.Contains(p.FailureReason(), "no record") {
			t.Errorf("status = %q reason = %q", p.Status(), p.FailureReason())
		}
	})

	t.Run("PSP lookup error leaves it for the next cycle", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-time.Hour))
		d := newRecoverDeps(p)
		d.psp.lookupErr = errBoom
		logs := d.run(t)
		if p.Status() != domain.StatusProcessing || len(d.repo.updates) != 0 || !strings.Contains(logs, "psp lookup") {
			t.Errorf("status = %q updates = %v logs = %s", p.Status(), d.repo.updates, logs)
		}
	})

	t.Run("resolved by the original request meanwhile: no error, no event", func(t *testing.T) {
		p := stuckPayment(recoverNow.Add(-5 * time.Minute))
		d := newRecoverDeps(p)
		d.psp.lookup = PSPCaptureLookup{Found: true, Approved: true, PSPReference: "PSP-77"}
		d.repo.changed = true
		logs := d.run(t)
		if len(d.pub.events) != 0 || strings.Contains(logs, "level=ERROR") {
			t.Errorf("events = %v logs = %s", d.pub.eventTypes(), logs)
		}
	})

	t.Run("list error is logged and the cycle ends", func(t *testing.T) {
		d := newRecoverDeps()
		d.repo.stuckErr = errBoom
		if logs := d.run(t); !strings.Contains(logs, "list stuck payments") {
			t.Errorf("logs = %s", logs)
		}
	})
}

func TestProcessingRecoverer_StartRunsFirstCycle(t *testing.T) {
	p := stuckPayment(recoverNow.Add(-5 * time.Minute))
	d := newRecoverDeps(p)
	d.psp.lookup = PSPCaptureLookup{Found: true, Approved: true, PSPReference: "PSP-1"}
	r := NewProcessingRecoverer(d.repo, &fakeRouter{psp: d.psp}, &fakeTx{}, d.pub, fixedClock{recoverNow},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Start(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // muy por debajo de recoverInterval
	cancel()
	<-done

	if p.Status() != domain.StatusCaptured {
		t.Fatalf("status = %q, want captured on start", p.Status())
	}
}
