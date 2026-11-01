package application

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var recoverNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// unresolvedPayout es un payout en status, sin cambios desde since.
func unresolvedPayout(status domain.PayoutStatus, since time.Time) *domain.Payout {
	return domain.ReconstitutePayoutWithIdempotency(
		domain.NewPayoutID(), domain.TenantID(tenantA),
		domain.ReconstituteMoney(5000, "ARS"), validBankAccount(),
		status, "", "", "payout_initiated_x", "k1",
		&since, nil, nil, since.Add(-time.Second), since, "",
	)
}

type recoverDeps struct {
	repo     *fakeRepo
	transfer *fakeTransfer
	pub      *fakePublisher
}

func newRecoverDeps(p ...*domain.Payout) *recoverDeps {
	return &recoverDeps{repo: &fakeRepo{unresolved: p}, transfer: &fakeTransfer{}, pub: &fakePublisher{}}
}

func (d *recoverDeps) run(t *testing.T) string {
	t.Helper()
	var logs strings.Builder
	NewPayoutRecoverer(d.repo, d.transfer, &fakeTx{}, d.pub, fixedClock{recoverNow},
		slog.New(slog.NewTextHandler(&logs, nil))).runCycle(context.Background())
	return logs.String()
}

// Los payouts unknown o trabados en processing se resuelven preguntándole al
// banco: antes quedaban en payout_transit indefinidamente.
func TestPayoutRecoverer(t *testing.T) {
	t.Run("asks only for payouts unchanged for longer than stuckAfter", func(t *testing.T) {
		d := newRecoverDeps()
		d.run(t)
		if want := recoverNow.Add(-stuckAfter); !d.repo.unresolvedBefore.Equal(want) {
			t.Errorf("olderThan = %v, want %v", d.repo.unresolvedBefore, want)
		}
	})

	for _, status := range []domain.PayoutStatus{domain.StatusUnknown, domain.StatusProcessing} {
		t.Run("completed at the bank confirms a "+status.String()+" payout", func(t *testing.T) {
			p := unresolvedPayout(status, recoverNow.Add(-5*time.Minute))
			d := newRecoverDeps(p)
			d.transfer.lookup = TransferLookup{Found: true, Completed: true, BankReference: "BANK-7"}

			d.run(t)

			if len(d.transfer.lookups) != 1 || d.transfer.lookups[0].IdempotencyKey != "transfer_"+p.ID().String() {
				t.Fatalf("lookups = %+v", d.transfer.lookups)
			}
			if p.Status() != domain.StatusConfirmed || p.BankReference() != "BANK-7" {
				t.Errorf("status = %q ref = %q", p.Status(), p.BankReference())
			}
			if !reflect.DeepEqual(d.repo.froms, []domain.PayoutStatus{status}) {
				t.Errorf("update froms = %v, want [%s]", d.repo.froms, status)
			}
			if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payout.confirmed.v1"}) {
				t.Errorf("events = %v", got)
			}
		})
	}

	t.Run("rejected at the bank fails the payout (the Ledger reverses it)", func(t *testing.T) {
		p := unresolvedPayout(domain.StatusUnknown, recoverNow.Add(-5*time.Minute))
		d := newRecoverDeps(p)
		d.transfer.lookup = TransferLookup{Found: true, Completed: false}
		d.run(t)
		if p.Status() != domain.StatusFailed || !reflect.DeepEqual(d.pub.eventTypes(), []string{"payout.failed.v1"}) {
			t.Errorf("status = %q events = %v", p.Status(), d.pub.eventTypes())
		}
	})

	t.Run("no record at the bank yet: waits", func(t *testing.T) {
		p := unresolvedPayout(domain.StatusUnknown, recoverNow.Add(-5*time.Minute))
		d := newRecoverDeps(p)
		d.run(t)
		if p.Status() != domain.StatusUnknown || len(d.repo.updates) != 0 {
			t.Errorf("status = %q updates = %v, want untouched", p.Status(), d.repo.updates)
		}
	})

	t.Run("no record at the bank after notFoundAfter: fails the payout", func(t *testing.T) {
		p := unresolvedPayout(domain.StatusProcessing, recoverNow.Add(-notFoundAfter))
		d := newRecoverDeps(p)
		d.run(t)
		if p.Status() != domain.StatusFailed || !strings.Contains(p.FailureReason(), "no record") {
			t.Errorf("status = %q reason = %q", p.Status(), p.FailureReason())
		}
	})

	t.Run("bank lookup error leaves it for the next cycle", func(t *testing.T) {
		p := unresolvedPayout(domain.StatusUnknown, recoverNow.Add(-time.Hour))
		d := newRecoverDeps(p)
		d.transfer.lookupErr = errBoom
		logs := d.run(t)
		if p.Status() != domain.StatusUnknown || len(d.repo.updates) != 0 || !strings.Contains(logs, "bank lookup") {
			t.Errorf("status = %q updates = %v logs = %s", p.Status(), d.repo.updates, logs)
		}
	})

	t.Run("resolved by the original request meanwhile: no error, no event", func(t *testing.T) {
		p := unresolvedPayout(domain.StatusProcessing, recoverNow.Add(-5*time.Minute))
		d := newRecoverDeps(p)
		d.transfer.lookup = TransferLookup{Found: true, Completed: true, BankReference: "BANK-7"}
		d.repo.changed = true
		logs := d.run(t)
		if len(d.pub.events) != 0 || strings.Contains(logs, "level=ERROR") {
			t.Errorf("events = %v logs = %s", d.pub.eventTypes(), logs)
		}
	})

	t.Run("list error is logged and the cycle ends", func(t *testing.T) {
		d := newRecoverDeps()
		d.repo.unresolvedErr = errBoom
		if logs := d.run(t); !strings.Contains(logs, "list unresolved payouts") {
			t.Errorf("logs = %s", logs)
		}
	})
}

func TestPayoutRecoverer_StartRunsFirstCycle(t *testing.T) {
	p := unresolvedPayout(domain.StatusUnknown, recoverNow.Add(-5*time.Minute))
	d := newRecoverDeps(p)
	d.transfer.lookup = TransferLookup{Found: true, Completed: true, BankReference: "B"}
	r := NewPayoutRecoverer(d.repo, d.transfer, &fakeTx{}, d.pub, fixedClock{recoverNow},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Start(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // muy por debajo de recoverInterval
	cancel()
	<-done

	if p.Status() != domain.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed on start", p.Status())
	}
}
