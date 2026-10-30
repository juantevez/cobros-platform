package nats

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/application"
	"github.com/juantevez/cobros-platform/context/audit/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeRepo struct {
	saved   []*domain.AuditLogEntry
	saveErr error
}

func (r *fakeRepo) Save(ctx context.Context, e *domain.AuditLogEntry) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, e)
	return nil
}
func (r *fakeRepo) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) { return nil, nil }
func (r *fakeRepo) LockChain(ctx context.Context) error                         { return nil }
func (r *fakeRepo) ExistsByEventID(ctx context.Context, id string) (bool, error) {
	for _, e := range r.saved {
		if e.EventID() == id {
			return true, nil
		}
	}
	return false, nil
}
func (r *fakeRepo) ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}
func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}
func (r *fakeRepo) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type sha256Hasher struct{}

func (sha256Hasher) Compute(p string) []byte { s := sha256.Sum256([]byte(p)); return s[:] }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }

// fakeConsumer captura la config con la que se lo arranca.
type fakeConsumer struct {
	cfg     eventbus.ConsumerConfig
	started bool
}

func (c *fakeConsumer) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	c.cfg = cfg
	c.started = true
	return nil
}

func newConsumer(repo *fakeRepo) (*EventConsumer, *fakeConsumer) {
	fc := &fakeConsumer{}
	uc := application.NewRecordActionUseCase(repo, sha256Hasher{}, fixedClock{}, fakeTx{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewEventConsumer(fc, uc, logger), fc
}

// ── mapEvent ──────────────────────────────────────────────────────────────────

func TestMapEvent(t *testing.T) {
	cases := []struct {
		subject      string
		wantAction   string
		wantResource string
		wantResID    string
		wantActor    string
	}{
		{"auth.tenant.created.v1", "auth.tenant.created", "tenant", "ten-1", "system"},
		{"auth.tenant.suspended.v1", "auth.tenant.suspended", "tenant", "ten-1", "system"},
		{"auth.user.registered.v1", "auth.user.registered", "user", "usr-1", "system"},
		{"auth.user.login.v1", "auth.user.login", "user", "usr-1", "usr-1"},
		{"auth.user.logout.v1", "auth.user.logout", "user", "usr-1", "usr-1"},
		{"auth.apikey.revoked.v1", "auth.apikey.revoked", "api_key", "key-1", "system"},
		{"auth.role.assigned.v1", "auth.role.assigned", "user", "usr-1", "admin-1"},
		{"ledger.entry.posted.v1", "ledger.entry.posted", "journal_entry", "ent-1", "system"},
		{"ledger.entry.reversed.v1", "ledger.entry.reversed", "journal_entry", "rev-1", "system"},
		{"onboarding.application.approved.v1", "onboarding.application.approved", "application", "app-1", "system"},
		{"payment.refunded.v1", "payment.refunded", "payment", "pay-1", "system"},
		{"payout.initiated.v1", "payout.initiated", "payout", "po-1", "system"},
		{"billing.plan.updated.v1", "billing.plan.updated", "billing_plan", "plan-1", "system"},
		{"billing.plan.assigned.v1", "billing.plan.assigned", "tenant_plan", "tp-1", "system"},
		{"dispute.resolved.v1", "dispute.resolved", "dispute", "dis-1", "system"},
		{"webhook.endpoint.registered.v1", "webhook.endpoint.registered", "webhook_endpoint", "ep-1", "system"},
		{"compliance.alert.review_proposed.v1", "compliance.alert.review_proposed", "aml_alert", "al-1", "system"},
		{"compliance.sanction.matched.v1", "compliance.sanction.matched", "aml_alert", "al-1", "system"},
		{"reconciliation.run.completed.v1", "reconciliation.run.completed", "reconciliation_run", "run-1", "system"},
	}

	payload := []byte(`{
		"tenant_id":"ten-1","user_id":"usr-1","api_key_id":"key-1","entry_id":"ent-1",
		"assigned_by":"admin-1","application_id":"app-1","payment_id":"pay-1","payout_id":"po-1",
		"plan_id":"plan-1","tenant_plan_id":"tp-1","dispute_id":"dis-1","endpoint_id":"ep-1",
		"alert_id":"al-1","run_id":"run-1","amount":1500,
		"reverse_entry_id":"rev-1","original_entry_id":"ent-0"
	}`)

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			ev, err := mapEvent(&eventbus.Message{Subject: tc.subject, Payload: payload})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Action != tc.wantAction || ev.ResourceType != tc.wantResource || ev.ResourceID != tc.wantResID {
				t.Errorf("got %s %s/%s, want %s %s/%s", ev.Action, ev.ResourceType, ev.ResourceID,
					tc.wantAction, tc.wantResource, tc.wantResID)
			}
			if ev.Actor != tc.wantActor {
				t.Errorf("Actor = %q, want %q", ev.Actor, tc.wantActor)
			}
			if ev.TenantID != "ten-1" || ev.Metadata["event_subject"] != tc.subject {
				t.Errorf("tenant/metadata mismatch: %+v", ev.RecordActionCmd)
			}
		})
	}

	t.Run("reversal keeps the original entry in metadata", func(t *testing.T) {
		ev, _ := mapEvent(&eventbus.Message{Subject: "ledger.entry.reversed.v1", Payload: payload})
		if ev.Metadata["original_entry_id"] != "ent-0" {
			t.Errorf("metadata = %+v", ev.Metadata)
		}
	})

	t.Run("action strips any version suffix", func(t *testing.T) {
		if got := versionSuffix.ReplaceAllString("auth.tenant.created.v12", ""); got != "auth.tenant.created" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unmapped subjects rejected", func(t *testing.T) {
		for _, subject := range []string{"payment.captured.v1", "webhook.delivery.exhausted.v1", "auth.tenant.created.v2"} {
			if _, err := mapEvent(&eventbus.Message{Subject: subject, Payload: []byte(`{}`)}); err == nil {
				t.Errorf("%s: expected error for unmapped subject", subject)
			}
		}
	})

	t.Run("invalid json rejected", func(t *testing.T) {
		_, err := mapEvent(&eventbus.Message{Subject: "auth.tenant.created.v1", Payload: []byte(`{bad`)})
		if err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
}

// subjectMatches implementa el matching de filtros NATS (* = un token, > = resto).
func subjectMatches(filter, subject string) bool {
	f, s := strings.Split(filter, "."), strings.Split(subject, ".")
	for i, tok := range f {
		if tok == ">" {
			return len(s) > i
		}
		if i >= len(s) || (tok != "*" && tok != s[i]) {
			return false
		}
	}
	return len(f) == len(s)
}

func TestCatalogConsistency(t *testing.T) {
	t.Run("every mapped subject is delivered by some subscription", func(t *testing.T) {
		for subject := range mappings {
			delivered := false
			for _, sub := range Subscriptions {
				if subjectMatches(sub.Filter, subject) {
					delivered = true
				}
			}
			if !delivered {
				t.Errorf("%s is mapped but no subscription filter delivers it", subject)
			}
		}
	})

	t.Run("every subscription delivers at least one mapped subject", func(t *testing.T) {
		for _, sub := range Subscriptions {
			found := false
			for subject := range mappings {
				if subjectMatches(sub.Filter, subject) {
					found = true
				}
			}
			if !found {
				t.Errorf("subscription %s (%s) has no mapped subject", sub.Name, sub.Filter)
			}
		}
	})

	t.Run("every mapped resource type is valid in the domain", func(t *testing.T) {
		for subject, m := range mappings {
			if _, err := domain.ParseResourceType(m.resourceType); err != nil {
				t.Errorf("%s: %v", subject, err)
			}
		}
	})

	t.Run("subscription names are unique", func(t *testing.T) {
		seen := map[string]bool{}
		for _, sub := range Subscriptions {
			if seen[sub.Name] {
				t.Errorf("duplicate consumer name %s", sub.Name)
			}
			seen[sub.Name] = true
		}
	})
}

// ── handle ────────────────────────────────────────────────────────────────────

func TestHandle(t *testing.T) {
	t.Run("known event is recorded", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			Subject: "auth.tenant.created.v1",
			Payload: []byte(`{"tenant_id":"ten-1"}`),
			Headers: map[string]string{"correlation-id": "corr-9"},
		}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.saved) != 1 {
			t.Fatalf("expected 1 saved entry, got %d", len(repo.saved))
		}
		if repo.saved[0].CorrelationID() != "corr-9" {
			t.Errorf("correlationID not propagated: %q", repo.saved[0].CorrelationID())
		}
	})

	t.Run("actor comes from the actor header", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			Subject: "auth.tenant.suspended.v1",
			Payload: []byte(`{"tenant_id":"ten-1"}`),
			Headers: map[string]string{eventbus.HeaderActor: "user-ops-7"},
		}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := repo.saved[0].Actor(); got != "user-ops-7" {
			t.Errorf("actor = %q, want user-ops-7", got)
		}
	})

	t.Run("without actor header keeps the mapped actor", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			Subject: "auth.role.assigned.v1",
			Payload: []byte(`{"tenant_id":"ten-1","user_id":"u-1","assigned_by":"admin-1"}`),
		}
		_ = c.handle(context.Background(), msg)
		if got := repo.saved[0].Actor(); got != "admin-1" {
			t.Errorf("actor = %q, want admin-1 (assigned_by)", got)
		}
	})

	t.Run("payload actor (login) wins over the header", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			Subject: "auth.user.login.v1",
			Payload: []byte(`{"tenant_id":"ten-1","user_id":"usr-1"}`),
			Headers: map[string]string{eventbus.HeaderActor: "system"},
		}
		_ = c.handle(context.Background(), msg)
		if got := repo.saved[0].Actor(); got != "usr-1" {
			t.Errorf("actor = %q, want usr-1", got)
		}
	})

	t.Run("unknown event is recorded as unmapped, never dropped", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			ID: "ev-x", Subject: "payment.captured.v1",
			Payload: []byte(`{"tenant_id":"ten-1","payment_id":"p-1"}`),
		}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unknown event should be acked (nil), got %v", err)
		}
		if len(repo.saved) != 1 {
			t.Fatalf("expected the event to be recorded, got %d entries", len(repo.saved))
		}
		e := repo.saved[0]
		if e.Action() != domain.ActionUnmappedEvent || e.ResourceType() != domain.ResourceEvent ||
			e.ResourceID() != "ev-x" || e.TenantID() != "ten-1" {
			t.Errorf("unexpected entry: %v", e)
		}
		if e.Metadata()["event_subject"] != "payment.captured.v1" || e.Metadata()["reason"] == "" {
			t.Errorf("metadata = %+v", e.Metadata())
		}
	})

	t.Run("invalid payload is recorded as unmapped", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{ID: "ev-y", Subject: "auth.tenant.created.v1", Payload: []byte(`{bad`)}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.saved) != 1 || repo.saved[0].Action() != domain.ActionUnmappedEvent {
			t.Fatalf("expected an unmapped entry, got %+v", repo.saved)
		}
	})

	t.Run("redelivery of the same event is recorded once", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{ID: "ev-1", Subject: "auth.tenant.created.v1", Payload: []byte(`{"tenant_id":"ten-1"}`)}
		for i := 0; i < 3; i++ {
			if err := c.handle(context.Background(), msg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if len(repo.saved) != 1 || repo.saved[0].EventID() != "ev-1" {
			t.Fatalf("expected a single entry for ev-1, got %d", len(repo.saved))
		}
	})

	t.Run("occurred-at header becomes the event time", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{
			ID: "ev-2", Subject: "auth.tenant.created.v1", Payload: []byte(`{"tenant_id":"ten-1"}`),
			Headers: map[string]string{eventbus.HeaderOccurredAt: "2026-07-06T11:59:30.123456789Z"},
		}
		_ = c.handle(context.Background(), msg)
		e := repo.saved[0]
		want := time.Date(2026, 7, 6, 11, 59, 30, 123456000, time.UTC) // truncado a µs
		if !e.OccurredAt().Equal(want) {
			t.Errorf("occurredAt = %v, want %v", e.OccurredAt(), want)
		}
		if !e.CreatedAt().Equal(fixedClock{}.Now()) {
			t.Errorf("createdAt = %v, want registration time", e.CreatedAt())
		}
	})

	t.Run("without occurred-at header the event time is the registration time", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		_ = c.handle(context.Background(), &eventbus.Message{ID: "ev-3", Subject: "auth.tenant.created.v1", Payload: []byte(`{}`)})
		if !repo.saved[0].OccurredAt().Equal(repo.saved[0].CreatedAt()) {
			t.Errorf("occurredAt = %v, createdAt = %v", repo.saved[0].OccurredAt(), repo.saved[0].CreatedAt())
		}
	})

	t.Run("record failure returns error for retry", func(t *testing.T) {
		repo := &fakeRepo{saveErr: errors.New("db down")}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{Subject: "auth.tenant.created.v1", Payload: []byte(`{"tenant_id":"ten-1"}`)}
		if err := c.handle(context.Background(), msg); err == nil {
			t.Fatal("expected error to trigger Nak/retry")
		}
	})

	t.Run("missing headers yields empty correlationID", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{Subject: "auth.tenant.created.v1", Payload: []byte(`{"tenant_id":"ten-1"}`)}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.saved[0].CorrelationID() != "" {
			t.Errorf("expected empty correlationID, got %q", repo.saved[0].CorrelationID())
		}
	})
}

// ── Start ─────────────────────────────────────────────────────────────────────

func TestStart(t *testing.T) {
	c, fc := newConsumer(&fakeRepo{})
	sub := Subscription{Stream: "PAYOUT", Filter: "payout.>", Name: "audit-payout-consumer"}
	if err := c.Start(context.Background(), sub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fc.started || fc.cfg.Stream != "PAYOUT" || fc.cfg.FilterSubject != "payout.>" ||
		fc.cfg.Name != "audit-payout-consumer" || fc.cfg.MaxDeliver != 5 {
		t.Errorf("unexpected config: %+v", fc.cfg)
	}
}

func TestSubscriptions_keepExistingDurables(t *testing.T) {
	// Los consumers durables ya existentes conservan su nombre: renombrarlos
	// reprocesaría todo el stream desde el principio.
	names := map[string]string{}
	for _, sub := range Subscriptions {
		names[sub.Stream] = sub.Name
	}
	if names["AUTH"] != "audit-auth-consumer" || names["LEDGER"] != "audit-ledger-consumer" {
		t.Errorf("existing durable names changed: %+v", names)
	}
}
