package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// Estos tests ejercitan los métodos del repositorio que hablan con la base
// usando pgxmock (sin Postgres real). Verifican el envío de queries/args y el
// mapeo de filas de vuelta a dominio, incluyendo el loop de scanEntries.

var repoCols = []string{
	"id", "tenant_id", "actor", "action", "resource_type", "resource_id",
	"metadata", "correlation_id", "prev_hash", "hash", "created_at",
}

func sha(p string) []byte { s := sha256.Sum256([]byte(p)); return s[:] }

func newMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock
}

// addRow carga una fila con los tipos que scanEntry espera.
func addRow(rows *pgxmock.Rows, id int64, tenantID *string, resourceID, correlationID *string, prevHash []byte) *pgxmock.Rows {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	return rows.AddRow(
		id, tenantID, "actor-1", "auth.user.login", "user", resourceID,
		[]byte(`{"ip":"10.0.0.1"}`), correlationID, prevHash, []byte{0xCC, 0xDD}, createdAt,
	)
}

func TestRepo_Save(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	entry, _ := domain.NewAuditLogEntry("tenant-1", "actor-1", domain.ActionLogin,
		domain.ResourceUser, "user-1", map[string]string{"ip": "10.0.0.1"}, "corr-1", nil, createdAt, sha)

	t.Run("inserts with mapped args", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)

		tenantID := "tenant-1"
		mock.ExpectExec("INSERT INTO audit_log").
			WithArgs(
				&tenantID, // *string, no nil porque el tenant no está vacío
				"actor-1", // actor
				"auth.user.login",
				"user",
				pgxmock.AnyArg(), // resource_id (*string)
				pgxmock.AnyArg(), // metadata json
				pgxmock.AnyArg(), // correlation_id (*string)
				pgxmock.AnyArg(), // prev_hash
				pgxmock.AnyArg(), // hash
				pgxmock.AnyArg(), // created_at
			).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(context.Background(), entry); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("empty tenant maps to nil arg", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		e, _ := domain.NewAuditLogEntry("", "system", domain.ActionTenantCreated,
			domain.ResourceTenant, "", nil, "", nil, createdAt, sha)

		mock.ExpectExec("INSERT INTO audit_log").
			WithArgs(
				(*string)(nil), // tenant_id nil
				"system",
				"auth.tenant.created",
				"tenant",
				(*string)(nil),   // resource_id nil (vacío)
				pgxmock.AnyArg(), // metadata
				(*string)(nil),   // correlation_id nil
				[]byte(nil),      // prev_hash nil (genesis)
				pgxmock.AnyArg(), // hash
				pgxmock.AnyArg(), // created_at
			).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Save(context.Background(), e); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectExec("INSERT INTO audit_log").WillReturnError(errors.New("boom"))

		if err := repo.Save(context.Background(), entry); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepo_FindLast(t *testing.T) {
	t.Run("returns last entry", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		tid := "tenant-1"
		rid := "user-1"
		rows := addRow(pgxmock.NewRows(repoCols), 42, &tid, &rid, nil, []byte{0xAA})
		mock.ExpectQuery("FOR UPDATE").WillReturnRows(rows)

		e, err := repo.FindLast(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e == nil || e.ID() != 42 || e.TenantID() != "tenant-1" {
			t.Errorf("unexpected entry: %+v", e)
		}
		if e.HashHex() != "ccdd" {
			t.Errorf("hash mismatch: %s", e.HashHex())
		}
	})

	t.Run("empty table returns nil,nil", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("FOR UPDATE").WillReturnRows(pgxmock.NewRows(repoCols))

		e, err := repo.FindLast(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e != nil {
			t.Errorf("expected nil entry, got %+v", e)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("FOR UPDATE").WillReturnError(errors.New("boom"))

		if _, err := repo.FindLast(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepo_ListRecent(t *testing.T) {
	t.Run("maps multiple rows", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		tid := "t"
		rows := pgxmock.NewRows(repoCols)
		addRow(rows, 2, &tid, nil, nil, []byte{0xBB})
		addRow(rows, 1, &tid, nil, nil, nil)
		mock.ExpectQuery("ORDER BY id DESC").WithArgs(50).WillReturnRows(rows)

		out, err := repo.ListRecent(context.Background(), 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 || out[0].ID() != 2 || out[1].ID() != 1 {
			t.Errorf("unexpected result: %+v", out)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("ORDER BY id DESC").WithArgs(50).WillReturnError(errors.New("boom"))

		if _, err := repo.ListRecent(context.Background(), 50); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error surfaces", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		// Fila con metadata JSON inválido: scanEntry falla dentro de scanEntries.
		rows := pgxmock.NewRows(repoCols).AddRow(
			int64(1), (*string)(nil), "actor", "auth.user.login", "user", (*string)(nil),
			[]byte(`{bad`), (*string)(nil), []byte(nil), []byte{0x01}, time.Now(),
		)
		mock.ExpectQuery("ORDER BY id DESC").WithArgs(50).WillReturnRows(rows)

		if _, err := repo.ListRecent(context.Background(), 50); err == nil {
			t.Fatal("expected scan error from invalid metadata")
		}
	})
}

func TestRepo_ListByTenant(t *testing.T) {
	t.Run("filters by tenant", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		tid := "tenant-9"
		rows := addRow(pgxmock.NewRows(repoCols), 5, &tid, nil, nil, nil)
		mock.ExpectQuery("WHERE tenant_id = \\$1").WithArgs("tenant-9", 20).WillReturnRows(rows)

		out, err := repo.ListByTenant(context.Background(), "tenant-9", 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 || out[0].TenantID() != "tenant-9" {
			t.Errorf("unexpected result: %+v", out)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("WHERE tenant_id = \\$1").WithArgs("t", 20).WillReturnError(errors.New("boom"))

		if _, err := repo.ListByTenant(context.Background(), "t", 20); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepo_ListFromID(t *testing.T) {
	t.Run("returns ascending from id", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		tid := "t"
		rows := pgxmock.NewRows(repoCols)
		addRow(rows, 10, &tid, nil, nil, nil)
		addRow(rows, 11, &tid, nil, nil, []byte{0xCC, 0xDD})
		mock.ExpectQuery("WHERE id >= \\$1").WithArgs(int64(10), 500).WillReturnRows(rows)

		out, err := repo.ListFromID(context.Background(), 10, 500)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 || out[0].ID() != 10 || out[1].ID() != 11 {
			t.Errorf("unexpected result: %+v", out)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("WHERE id >= \\$1").WithArgs(int64(0), 500).WillReturnRows(pgxmock.NewRows(repoCols))

		out, err := repo.ListFromID(context.Background(), 0, 500)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("expected empty, got %d", len(out))
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewAuditLogRepository(mock)
		mock.ExpectQuery("WHERE id >= \\$1").WithArgs(int64(0), 500).WillReturnError(errors.New("boom"))

		if _, err := repo.ListFromID(context.Background(), 0, 500); err == nil {
			t.Fatal("expected error")
		}
	})
}
