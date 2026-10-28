package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// Tests con pgxmock (no requieren PostgreSQL): validan el SQL idempotente y
// tolerante al orden de place/release.
func TestComplianceHoldRepository(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	tid := domain.NewTenantID()
	alertID := uuid.NewString()

	newRepo := func(t *testing.T) (*pgComplianceHoldRepository, pgxmock.PgxPoolIface) {
		t.Helper()
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("mock: %v", err)
		}
		t.Cleanup(mock.Close)
		return NewComplianceHoldRepository(mock), mock
	}

	t.Run("place is idempotent and never reactivates", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`INSERT INTO tenant_compliance_holds .* ON CONFLICT \(tenant_id, alert_id\) DO NOTHING`).
			WithArgs(tid.String(), alertID, at).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := repo.Place(context.Background(), tid, alertID, at); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("release upserts and keeps the first release time", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`INSERT INTO tenant_compliance_holds .* DO UPDATE SET released_at = COALESCE\(tenant_compliance_holds.released_at, EXCLUDED.released_at\)`).
			WithArgs(tid.String(), alertID, at).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := repo.Release(context.Background(), tid, alertID, at); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db errors propagated", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec("INSERT INTO tenant_compliance_holds").WillReturnError(errors.New("boom"))
		mock.ExpectExec("INSERT INTO tenant_compliance_holds").WillReturnError(errors.New("boom"))
		if err := repo.Place(context.Background(), tid, alertID, at); err == nil {
			t.Error("place: expected error")
		}
		if err := repo.Release(context.Background(), tid, alertID, at); err == nil {
			t.Error("release: expected error")
		}
	})
}
