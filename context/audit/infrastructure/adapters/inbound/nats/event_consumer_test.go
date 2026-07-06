package nats

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
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
func (r *fakeRepo) ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}
func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}
func (r *fakeRepo) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	return nil, nil
}

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
	uc := application.NewRecordActionUseCase(repo, sha256Hasher{}, fixedClock{})
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
		{"auth.tenant.activated.v1", "auth.tenant.activated", "tenant", "ten-1", "system"},
		{"auth.tenant.suspended.v1", "auth.tenant.suspended", "tenant", "ten-1", "system"},
		{"auth.user.registered.v1", "auth.user.registered", "user", "usr-1", "system"},
		{"auth.user.suspended.v1", "auth.user.suspended", "user", "usr-1", "system"},
		{"auth.apikey.issued.v1", "auth.apikey.issued", "api_key", "key-1", "system"},
		{"auth.apikey.revoked.v1", "auth.apikey.revoked", "api_key", "key-1", "system"},
		{"auth.role.assigned.v1", "auth.role.assigned", "user", "usr-1", "admin-1"},
		{"ledger.account.created.v1", "ledger.account.created", "ledger_account", "acc-1", "system"},
		{"ledger.entry.posted.v1", "ledger.entry.posted", "journal_entry", "ent-1", "system"},
		{"ledger.entry.reversed.v1", "ledger.entry.reversed", "journal_entry", "ent-1", "system"},
	}

	payload := []byte(`{
		"tenant_id":"ten-1","user_id":"usr-1","api_key_id":"key-1",
		"entry_id":"ent-1","account_id":"acc-1","assigned_by":"admin-1"
	}`)

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			cmd, err := mapEvent(&eventbus.Message{Subject: tc.subject, Payload: payload})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", cmd.Action, tc.wantAction)
			}
			if cmd.ResourceType != tc.wantResource {
				t.Errorf("ResourceType = %q, want %q", cmd.ResourceType, tc.wantResource)
			}
			if cmd.ResourceID != tc.wantResID {
				t.Errorf("ResourceID = %q, want %q", cmd.ResourceID, tc.wantResID)
			}
			if cmd.Actor != tc.wantActor {
				t.Errorf("Actor = %q, want %q", cmd.Actor, tc.wantActor)
			}
			if cmd.TenantID != "ten-1" {
				t.Errorf("TenantID = %q, want ten-1", cmd.TenantID)
			}
			if cmd.Metadata["event_subject"] != tc.subject {
				t.Errorf("Metadata event_subject = %q", cmd.Metadata["event_subject"])
			}
		})
	}

	t.Run("unknown subject rejected", func(t *testing.T) {
		_, err := mapEvent(&eventbus.Message{Subject: "payment.captured.v1", Payload: []byte(`{}`)})
		if err == nil {
			t.Fatal("expected error for unmapped subject")
		}
	})

	t.Run("invalid json rejected", func(t *testing.T) {
		_, err := mapEvent(&eventbus.Message{Subject: "auth.tenant.created.v1", Payload: []byte(`{bad`)})
		if err == nil {
			t.Fatal("expected unmarshal error")
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

	t.Run("unknown event is acked and skipped", func(t *testing.T) {
		repo := &fakeRepo{}
		c, _ := newConsumer(repo)
		msg := &eventbus.Message{Subject: "payment.captured.v1", Payload: []byte(`{}`)}
		if err := c.handle(context.Background(), msg); err != nil {
			t.Fatalf("unknown event should be acked (nil), got %v", err)
		}
		if len(repo.saved) != 0 {
			t.Error("nothing should be recorded for unknown event")
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

// ── Start*Consumer ────────────────────────────────────────────────────────────

func TestStartConsumers(t *testing.T) {
	t.Run("auth consumer config", func(t *testing.T) {
		c, fc := newConsumer(&fakeRepo{})
		if err := c.StartAuthConsumer(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !fc.started {
			t.Fatal("consumer not started")
		}
		if fc.cfg.Stream != "AUTH" || fc.cfg.FilterSubject != "auth.>" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
		if fc.cfg.Name != "audit-auth-consumer" || fc.cfg.MaxDeliver != 5 {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
	})

	t.Run("ledger consumer config", func(t *testing.T) {
		c, fc := newConsumer(&fakeRepo{})
		if err := c.StartLedgerConsumer(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fc.cfg.Stream != "LEDGER" || fc.cfg.FilterSubject != "ledger.>" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
		if fc.cfg.Name != "audit-ledger-consumer" {
			t.Errorf("unexpected config: %+v", fc.cfg)
		}
	})
}
