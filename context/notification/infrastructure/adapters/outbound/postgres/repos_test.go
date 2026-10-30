package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

func newMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock
}

// expectSession espera la tx efímera de RunWithAppTenant que fija
// app.current_tenant antes de la consulta (RLS).
func expectSession(mock pgxmock.PgxPoolIface) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectExec("set_config").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("SELECT", 1))
}

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

var (
	testTime  = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	notifCols = []string{"id", "tenant_id", "event_type", "channel", "recipient_email", "subject", "status", "error_msg", "sent_at", "created_at"}
	prefCols  = []string{"event_type", "channel", "enabled", "recipient_email", "updated_at"}
	findCols  = []string{"channel", "enabled", "recipient_email", "updated_at"}
)

// ── NotificationRepository ────────────────────────────────────────────────────

func TestNotificationRepository_Save(t *testing.T) {
	n := domain.NewNotification(domain.NewNotificationID(), domain.TenantID(uuid.NewString()),
		"payment.captured", domain.ChannelEmail, "u@x.com", "Pago", time.Now())

	t.Run("inserts notification", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewNotificationRepository(mock)
		mock.ExpectExec("INSERT INTO notifications").WithArgs(anyArgs(11)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		mock.ExpectCommit()
		if err := repo.Save(context.Background(), n); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("insert error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewNotificationRepository(mock)
		mock.ExpectExec("INSERT INTO notifications").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if err := repo.Save(context.Background(), n); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestNotificationRepository_ListByTenant(t *testing.T) {
	tid := domain.TenantID(uuid.NewString())

	t.Run("returns notifications", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewNotificationRepository(mock)
		id := domain.NewNotificationID()
		mock.ExpectQuery("FROM notifications").WithArgs(tid.String(), 50).
			WillReturnRows(pgxmock.NewRows(notifCols).AddRow(
				id.String(), tid.String(), "payment.captured", "email",
				"u@x.com", "Pago", "sent", "", &testTime, testTime))

		mock.ExpectCommit()
		out, err := repo.ListByTenant(context.Background(), tid, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
		if out[0].ID() != id || out[0].Status() != domain.StatusSent {
			t.Errorf("unexpected notification: %+v", out[0])
		}
		if out[0].SentAt() == nil || !out[0].SentAt().Equal(testTime) {
			t.Errorf("sentAt not scanned: %v", out[0].SentAt())
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewNotificationRepository(mock)
		mock.ExpectQuery("FROM notifications").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := repo.ListByTenant(context.Background(), tid, 50); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error surfaces", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewNotificationRepository(mock)
		// created_at con tipo inválido → error de scan.
		mock.ExpectQuery("FROM notifications").WithArgs(tid.String(), 50).
			WillReturnRows(pgxmock.NewRows(notifCols).AddRow(
				"id", tid.String(), "e", "email", "u@x.com", "s", "sent", "", (*time.Time)(nil), "not-a-time"))
		mock.ExpectRollback()
		if _, err := repo.ListByTenant(context.Background(), tid, 50); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// ── PreferenceRepository ──────────────────────────────────────────────────────

func TestNotificationRepository_ListByTenant_rejectsCorruptRows(t *testing.T) {
	tid := domain.TenantID(uuid.NewString())
	for name, row := range map[string][]any{
		"unknown status":  {"id-1", tid.String(), "payment.captured", "email", "u@x.com", "s", "delivered", "", (*time.Time)(nil), testTime},
		"unknown channel": {"id-1", tid.String(), "payment.captured", "sms", "u@x.com", "s", "sent", "", (*time.Time)(nil), testTime},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newMock(t)
			expectSession(mock)
			mock.ExpectQuery("FROM notifications").WithArgs(tid.String(), 50).
				WillReturnRows(pgxmock.NewRows(notifCols).AddRow(row...))
			mock.ExpectRollback()
			if _, err := NewNotificationRepository(mock).ListByTenant(context.Background(), tid, 50); err == nil {
				t.Fatal("expected error for a corrupt row")
			}
		})
	}
}

func TestPreferenceRepository_Upsert(t *testing.T) {
	p := domain.NewNotificationPreference(domain.TenantID(uuid.NewString()),
		"payment.captured", domain.ChannelEmail, true, "ops@x.com", time.Now())

	t.Run("upserts preference", func(t *testing.T) {
		mock := newMock(t)
		repo := NewPreferenceRepository(mock)
		// RLS: tx efímera con app.current_tenant fijado antes del upsert.
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(rlsTenantKey, p.TenantID().String()).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec("INSERT INTO notification_preferences").WithArgs(anyArgs(6)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()

		if err := repo.Upsert(context.Background(), p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("upsert error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewPreferenceRepository(mock)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec("INSERT INTO notification_preferences").WithArgs(anyArgs(6)...).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if err := repo.Upsert(context.Background(), p); err == nil {
			t.Fatal("expected error")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})
}

func TestPreferenceRepository_FindByTenantAndEvent(t *testing.T) {
	tid := domain.TenantID(uuid.NewString())

	t.Run("found returns preference", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WithArgs(tid.String(), "payment.captured").
			WillReturnRows(pgxmock.NewRows(findCols).AddRow("email", true, "ops@x.com", testTime))

		mock.ExpectCommit()
		p, err := repo.FindByTenantAndEvent(context.Background(), tid, "payment.captured")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.Enabled() || p.RecipientEmail() != "ops@x.com" || p.EventType() != "payment.captured" {
			t.Errorf("unexpected preference: %+v", p)
		}
	})

	t.Run("no rows maps to ErrPreferenceNotFound", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WithArgs(tid.String(), "payment.captured").
			WillReturnError(pgx.ErrNoRows)

		mock.ExpectRollback()
		_, err := repo.FindByTenantAndEvent(context.Background(), tid, "payment.captured")
		if !errors.Is(err, domain.ErrPreferenceNotFound) {
			t.Fatalf("expected ErrPreferenceNotFound, got %v", err)
		}
	})

	t.Run("other error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WithArgs(tid.String(), "payment.captured").
			WillReturnError(errors.New("boom"))

		mock.ExpectRollback()
		_, err := repo.FindByTenantAndEvent(context.Background(), tid, "payment.captured")
		if err == nil || errors.Is(err, domain.ErrPreferenceNotFound) {
			t.Fatalf("expected generic error, got %v", err)
		}
	})
}

func TestPreferenceRepository_ListByTenant(t *testing.T) {
	tid := domain.TenantID(uuid.NewString())

	t.Run("returns preferences", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WithArgs(tid.String()).
			WillReturnRows(pgxmock.NewRows(prefCols).
				AddRow("payment.captured", "email", true, "a@x.com", testTime).
				AddRow("payment.failed", "email", false, "", testTime))

		mock.ExpectCommit()
		out, err := repo.ListByTenant(context.Background(), tid)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 {
			t.Fatalf("expected 2, got %d", len(out))
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := repo.ListByTenant(context.Background(), tid); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error surfaces", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		repo := NewPreferenceRepository(mock)
		mock.ExpectQuery("FROM notification_preferences").WithArgs(tid.String()).
			WillReturnRows(pgxmock.NewRows(prefCols).AddRow("e", "email", "not-a-bool", "", testTime))
		mock.ExpectRollback()
		if _, err := repo.ListByTenant(context.Background(), tid); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// ── ContactReader ─────────────────────────────────────────────────────────────

func TestContactReader_GetTenantAdminEmail(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns admin email", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		reader := NewContactReader(mock)
		mock.ExpectQuery("FROM users").WithArgs(tid).
			WillReturnRows(pgxmock.NewRows([]string{"email"}).AddRow("admin@x.com"))

		mock.ExpectCommit()
		email, err := reader.GetTenantAdminEmail(context.Background(), tid)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if email != "admin@x.com" {
			t.Errorf("email = %q", email)
		}
	})

	t.Run("no rows maps to ErrNoRecipientEmail", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		reader := NewContactReader(mock)
		mock.ExpectQuery("FROM users").WithArgs(tid).WillReturnError(pgx.ErrNoRows)

		mock.ExpectRollback()
		_, err := reader.GetTenantAdminEmail(context.Background(), tid)
		if !errors.Is(err, domain.ErrNoRecipientEmail) {
			t.Fatalf("expected ErrNoRecipientEmail, got %v", err)
		}
	})

	t.Run("other error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectSession(mock)
		reader := NewContactReader(mock)
		mock.ExpectQuery("FROM users").WithArgs(tid).WillReturnError(errors.New("boom"))

		mock.ExpectRollback()
		_, err := reader.GetTenantAdminEmail(context.Background(), tid)
		if err == nil || errors.Is(err, domain.ErrNoRecipientEmail) {
			t.Fatalf("expected generic error, got %v", err)
		}
	})
}

func TestNullStr(t *testing.T) {
	if nullStr("") != nil {
		t.Error("empty should map to nil")
	}
	if got := nullStr("x"); got == nil || *got != "x" {
		t.Errorf("non-empty mapped wrong: %v", got)
	}
}
