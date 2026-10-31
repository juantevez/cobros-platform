package application

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

type retryDeps struct {
	endpoint   *domain.WebhookEndpoint
	endpoints  *fakeEndpointRepo
	deliveries *fakeDeliveryRepo
	dispatcher *fakeDispatcher
	tx         *fakeTx
	pub        *fakePublisher
}

func newRetryDeps(seed ...*domain.WebhookDelivery) *retryDeps {
	e := newEndpoint(tenantA)
	return &retryDeps{
		endpoint:   e,
		endpoints:  newEndpointRepo(e),
		deliveries: newDeliveryRepo(seed...),
		dispatcher: &fakeDispatcher{status: 200},
		tx:         &fakeTx{},
		pub:        &fakePublisher{},
	}
}

func (d *retryDeps) uc() *RetryDeliveryUseCase {
	return NewRetryDeliveryUseCase(d.endpoints, d.deliveries, d.dispatcher, d.tx, d.pub, fixedClock{testNow})
}

// seed agrega una delivery del endpoint de los deps al repo.
func (d *retryDeps) seed(status domain.DeliveryStatus, attempts int) *domain.WebhookDelivery {
	del := deliveryWith(d.endpoint, status, attempts)
	d.deliveries.byID[del.ID()] = del
	return del
}

func TestRetryDeliveryUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("successful dispatch marks delivery delivered", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusFailed, 1)

		if err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.dispatcher.delivered) != 1 || d.dispatcher.delivered[0] != del || d.dispatcher.endpoints[0] != d.endpoint {
			t.Fatal("dispatcher should receive the delivery and its endpoint")
		}
		if del.Status() != domain.StatusDelivered || del.AttemptCount() != 2 || del.NextRetryAt() != nil {
			t.Errorf("status=%q attempts=%d next=%v", del.Status(), del.AttemptCount(), del.NextRetryAt())
		}
		if !del.UpdatedAt().Equal(testNow) {
			t.Errorf("updatedAt = %v, want clock %v", del.UpdatedAt(), testNow)
		}
		if !reflect.DeepEqual(d.deliveries.updates, []domain.DeliveryStatus{domain.StatusDelivered}) {
			t.Errorf("updates = %v", d.deliveries.updates)
		}
		if d.pub.calls != 0 {
			t.Error("no event should be published on success")
		}
	})

	t.Run("failed dispatch schedules next retry from clock", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.status = 500
		del := d.seed(domain.StatusPending, 0)

		if err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if del.Status() != domain.StatusFailed || del.NextRetryAt() == nil || !del.NextRetryAt().Equal(testNow.Add(30*time.Second)) {
			t.Errorf("status=%q next=%v", del.Status(), del.NextRetryAt())
		}
		if d.pub.calls != 0 {
			t.Error("no event should be published while retries remain")
		}
	})

	t.Run("last failed attempt exhausts delivery and publishes event", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.status = 503
		del := d.seed(domain.StatusFailed, domain.MaxAttempts-1)

		if err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if del.Status() != domain.StatusExhausted || del.NextRetryAt() != nil {
			t.Errorf("status=%q next=%v", del.Status(), del.NextRetryAt())
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"webhook.delivery.exhausted.v1"}) {
			t.Errorf("published = %v", got)
		}
	})

	// Antes el error de publish se descartaba: la delivery quedaba exhausted
	// sin su evento. Ahora el error sale del RunInTx y la tx se descarta.
	t.Run("publish error of exhausted event fails the transaction", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.status = 503
		d.pub.err = errBoom
		del := d.seed(domain.StatusFailed, domain.MaxAttempts-1)

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "publish delivery events") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.pub.calls != 1 || d.tx.calls != 1 {
			t.Errorf("publish calls = %d tx calls = %d, want 1 and 1", d.pub.calls, d.tx.calls)
		}
	})

	// El poller, el reintento manual y otra réplica del worker podían
	// despachar la misma delivery: ahora se reclama antes del POST.
	t.Run("claims the delivery before dispatching and guards the update", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusFailed, 2)

		if err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []claimCall{{tenant: tenantA, id: del.ID(), attemptCount: 2, now: testNow, until: testNow.Add(claimLease)}}
		if !reflect.DeepEqual(d.deliveries.claims, want) {
			t.Errorf("claims = %+v, want %+v", d.deliveries.claims, want)
		}
		if !reflect.DeepEqual(d.deliveries.prevAttempts, []int{2}) {
			t.Errorf("update prevAttempts = %v, want [2]", d.deliveries.prevAttempts)
		}
	})

	t.Run("delivery claimed by another process is not dispatched", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusPending, 0)
		d.deliveries.notClaimable = map[domain.DeliveryID]bool{del.ID(): true}

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, domain.ErrDeliveryInProgress) {
			t.Fatalf("expected ErrDeliveryInProgress, got %v", err)
		}
		if len(d.dispatcher.delivered) != 0 || len(d.deliveries.updates) != 0 {
			t.Errorf("dispatched = %d updates = %d, want none", len(d.dispatcher.delivered), len(d.deliveries.updates))
		}
	})

	t.Run("claim error is wrapped and nothing is dispatched", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusPending, 0)
		d.deliveries.claimErr = errBoom

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "claim delivery") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.dispatcher.delivered) != 0 {
			t.Error("dispatcher must not be called")
		}
	})

	t.Run("update and event are persisted in one transaction", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.status = 503
		del := d.seed(domain.StatusFailed, domain.MaxAttempts-1)

		if err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.tx.calls != 1 || !d.deliveries.updatedInTx || !d.pub.publishedInTx {
			t.Errorf("tx calls = %d update in tx = %v publish in tx = %v", d.tx.calls, d.deliveries.updatedInTx, d.pub.publishedInTx)
		}
	})

	for _, st := range []domain.DeliveryStatus{domain.StatusDelivered, domain.StatusExhausted} {
		t.Run("final status "+st.String()+" is not retryable", func(t *testing.T) {
			d := newRetryDeps()
			del := d.seed(st, 1)

			err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
			if !errors.Is(err, domain.ErrDeliveryNotRetryable) {
				t.Fatalf("expected ErrDeliveryNotRetryable, got %v", err)
			}
			if len(d.dispatcher.delivered) != 0 {
				t.Error("dispatcher must not be called")
			}
		})
	}

	t.Run("delivery of another tenant is reported as not found", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusFailed, 1)

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantB, DeliveryID: del.ID().String()})
		if !errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected ErrDeliveryNotFound, got %v", err)
		}
		if len(d.dispatcher.delivered) != 0 {
			t.Error("dispatcher must not be called")
		}
	})

	t.Run("unknown delivery returns ErrDeliveryNotFound", func(t *testing.T) {
		d := newRetryDeps()
		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: domain.NewDeliveryID().String()})
		if !errors.Is(err, domain.ErrDeliveryNotFound) {
			t.Fatalf("expected ErrDeliveryNotFound, got %v", err)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		err := newRetryDeps().uc().Execute(ctx, RetryDeliveryCmd{TenantID: "x", DeliveryID: domain.NewDeliveryID().String()})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("invalid delivery id", func(t *testing.T) {
		err := newRetryDeps().uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid delivery id") {
			t.Fatalf("expected invalid delivery error, got %v", err)
		}
	})

	t.Run("inactive endpoint is not dispatched", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusFailed, 1)
		d.endpoint.Deactivate()

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, domain.ErrEndpointInactive) {
			t.Fatalf("expected ErrEndpointInactive, got %v", err)
		}
		if d.dispatcher.calls() != 0 || len(d.deliveries.updates) != 0 {
			t.Error("nothing should be dispatched or updated")
		}
		if del.Status() != domain.StatusFailed || del.AttemptCount() != 1 {
			t.Errorf("delivery must stay as is: %q %d", del.Status(), del.AttemptCount())
		}
	})

	t.Run("endpoint load error is wrapped", func(t *testing.T) {
		d := newRetryDeps()
		del := d.seed(domain.StatusFailed, 1)
		d.endpoints.findErr = errBoom

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "load endpoint") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.dispatcher.delivered) != 0 {
			t.Error("dispatcher must not be called")
		}
	})

	t.Run("update error is wrapped and nothing is published", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.status = 503
		d.deliveries.updateErr = errBoom
		del := d.seed(domain.StatusFailed, domain.MaxAttempts-1)

		err := d.uc().Execute(ctx, RetryDeliveryCmd{TenantID: tenantA, DeliveryID: del.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update delivery") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("event must not be published when update fails")
		}
	})
}

// ── RetryPoller ───────────────────────────────────────────────────────────────

func TestNewRetryPoller_DefaultInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		if p := NewRetryPoller(nil, newDeliveryRepo(), interval, discardLogger()); p.interval != 5*time.Second {
			t.Errorf("interval %v → %v, want 5s", interval, p.interval)
		}
	}
	if p := NewRetryPoller(nil, newDeliveryRepo(), time.Minute, discardLogger()); p.interval != time.Minute {
		t.Errorf("explicit interval not kept: %v", p.interval)
	}
}

func TestRetryPoller_RunCycle(t *testing.T) {
	ctx := context.Background()

	t.Run("dispatches every due delivery", func(t *testing.T) {
		d := newRetryDeps()
		due := []*domain.WebhookDelivery{deliveryWith(d.endpoint, domain.StatusPending, 0), deliveryWith(d.endpoint, domain.StatusFailed, 2)}
		d.deliveries.due = due

		before := time.Now().UTC()
		NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)

		if d.deliveries.lastLimit != retryBatchSize || d.deliveries.lastDueNow.Before(before) {
			t.Errorf("ListDueForRetry(now=%v, limit=%d)", d.deliveries.lastDueNow, d.deliveries.lastLimit)
		}
		if len(d.dispatcher.delivered) != 2 {
			t.Fatalf("dispatched %d, want 2", len(d.dispatcher.delivered))
		}
		for _, del := range due {
			if del.Status() != domain.StatusDelivered {
				t.Errorf("delivery %s status = %q", del.ID(), del.Status())
			}
		}
	})

	t.Run("dispatch error on one delivery does not stop the rest", func(t *testing.T) {
		d := newRetryDeps()
		orphan := deliveryWith(newEndpoint(tenantA), domain.StatusPending, 0) // endpoint no está en el repo
		ok := deliveryWith(d.endpoint, domain.StatusPending, 0)
		d.deliveries.due = []*domain.WebhookDelivery{orphan, ok}

		NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)

		if ok.Status() != domain.StatusDelivered {
			t.Errorf("second delivery status = %q, want delivered", ok.Status())
		}
	})

	t.Run("delivery claimed by another process is skipped without error log", func(t *testing.T) {
		d := newRetryDeps()
		taken := deliveryWith(d.endpoint, domain.StatusPending, 0)
		free := deliveryWith(d.endpoint, domain.StatusPending, 0)
		d.deliveries.due = []*domain.WebhookDelivery{taken, free}
		d.deliveries.notClaimable = map[domain.DeliveryID]bool{taken.ID(): true}

		var logs strings.Builder
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		NewRetryPoller(d.uc(), d.deliveries, time.Second, logger).runCycle(ctx)

		if len(d.dispatcher.delivered) != 1 || d.dispatcher.delivered[0] != free {
			t.Errorf("dispatched %d deliveries, want only the free one", len(d.dispatcher.delivered))
		}
		if strings.Contains(logs.String(), "level=ERROR") {
			t.Errorf("unexpected error log: %s", logs.String())
		}
	})

	// El poller arranca sin tenant (lista como operador); cada despacho debe
	// correr con el tenant de su delivery para pasar RLS.
	t.Run("each dispatch runs with the tenant of its delivery", func(t *testing.T) {
		d := newRetryDeps()
		otherEndpoint := newEndpoint(tenantB)
		d.endpoints.byID[otherEndpoint.ID()] = otherEndpoint
		a := deliveryWith(d.endpoint, domain.StatusPending, 0)
		b := deliveryWith(otherEndpoint, domain.StatusPending, 0)
		d.deliveries.due = []*domain.WebhookDelivery{a, b}

		NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)

		got := map[domain.DeliveryID]string{}
		for _, c := range d.deliveries.claims {
			got[c.id] = c.tenant
		}
		if got[a.ID()] != tenantA || got[b.ID()] != tenantB {
			t.Errorf("claim tenants = %v, want %s→%s and %s→%s", got, a.ID(), tenantA, b.ID(), tenantB)
		}
	})

	t.Run("dispatches in parallel up to the concurrency limit", func(t *testing.T) {
		d := newRetryDeps()
		d.dispatcher.block = make(chan struct{})
		for i := 0; i < retryConcurrency+5; i++ {
			d.deliveries.due = append(d.deliveries.due, deliveryWith(d.endpoint, domain.StatusPending, 0))
		}

		done := make(chan struct{})
		go func() {
			NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)
			close(done)
		}()

		// Esperar a que se llenen los slots: con despacho secuencial nunca
		// habría más de uno en vuelo.
		deadline := time.After(2 * time.Second)
		for d.dispatcher.calls() < retryConcurrency {
			select {
			case <-deadline:
				t.Fatalf("only %d dispatches started, want %d in parallel", d.dispatcher.calls(), retryConcurrency)
			case <-time.After(time.Millisecond):
			}
		}
		close(d.dispatcher.block)
		<-done

		if d.dispatcher.calls() != retryConcurrency+5 {
			t.Errorf("dispatched %d, want %d", d.dispatcher.calls(), retryConcurrency+5)
		}
		if d.dispatcher.maxFlight != retryConcurrency {
			t.Errorf("max in flight = %d, want %d", d.dispatcher.maxFlight, retryConcurrency)
		}
	})

	t.Run("list error skips the cycle", func(t *testing.T) {
		d := newRetryDeps()
		d.deliveries.dueErr = errBoom
		d.deliveries.due = []*domain.WebhookDelivery{deliveryWith(d.endpoint, domain.StatusPending, 0)}

		NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)

		if len(d.dispatcher.delivered) != 0 {
			t.Error("nothing should be dispatched")
		}
	})

	t.Run("no due deliveries does nothing", func(t *testing.T) {
		d := newRetryDeps()
		NewRetryPoller(d.uc(), d.deliveries, time.Second, discardLogger()).runCycle(ctx)
		if len(d.dispatcher.delivered) != 0 {
			t.Error("nothing should be dispatched")
		}
	})
}

func TestRetryPoller_Start(t *testing.T) {
	d := newRetryDeps()
	p := NewRetryPoller(d.uc(), d.deliveries, time.Millisecond, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()

	deadline := time.After(2 * time.Second)
	for d.deliveries.dueCallCount() < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("poller did not tick")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}
