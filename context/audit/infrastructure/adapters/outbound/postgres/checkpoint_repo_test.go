package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

func TestCheckpointRepository(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	t.Run("save", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectExec("INSERT INTO audit_chain_checkpoints").WithArgs(int64(42), []byte{0xAB}, at).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := NewCheckpointRepository(mock).Save(context.Background(),
			domain.ChainCheckpoint{EntryID: 42, EntryHash: []byte{0xAB}, CreatedAt: at}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("latest and empty", func(t *testing.T) {
		mock := newMock(t)
		cols := []string{"id", "entry_id", "entry_hash", "created_at"}
		mock.ExpectQuery("FROM audit_chain_checkpoints\\s+ORDER BY id DESC").
			WillReturnRows(pgxmock.NewRows(cols).AddRow(int64(3), int64(42), []byte{0xAB}, at))
		cp, err := NewCheckpointRepository(mock).Latest(context.Background())
		if err != nil || cp == nil || cp.EntryID != 42 || cp.EntryHashHex() != "ab" {
			t.Fatalf("got %+v, %v", cp, err)
		}
		mock.ExpectQuery("FROM audit_chain_checkpoints").WillReturnRows(pgxmock.NewRows(cols))
		if cp, err := NewCheckpointRepository(mock).Latest(context.Background()); cp != nil || err != nil {
			t.Fatalf("empty: got %+v, %v", cp, err)
		}
	})

	t.Run("first mismatch: missing entry, changed hash, none", func(t *testing.T) {
		cols := []string{"id", "entry_id", "hash"}
		q := `LEFT JOIN audit_log a ON a.id = c.entry_id\s+WHERE a.id IS NULL OR a.hash <> c.entry_hash`

		mock := newMock(t)
		mock.ExpectQuery(q).WillReturnRows(pgxmock.NewRows(cols).AddRow(int64(7), int64(42), []byte(nil)))
		m, err := NewCheckpointRepository(mock).FirstMismatch(context.Background())
		if err != nil || m == nil || !m.EntryMissing || m.EntryID != 42 || m.CheckpointID != 7 {
			t.Fatalf("missing: got %+v, %v", m, err)
		}

		mock.ExpectQuery(q).WillReturnRows(pgxmock.NewRows(cols).AddRow(int64(7), int64(42), []byte{0x01}))
		if m, _ := NewCheckpointRepository(mock).FirstMismatch(context.Background()); m == nil || m.EntryMissing {
			t.Fatalf("changed: got %+v", m)
		}

		mock.ExpectQuery(q).WillReturnRows(pgxmock.NewRows(cols))
		if m, err := NewCheckpointRepository(mock).FirstMismatch(context.Background()); m != nil || err != nil {
			t.Fatalf("none: got %+v, %v", m, err)
		}
	})

	t.Run("db errors propagated", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery("audit_chain_checkpoints").WillReturnError(errors.New("boom"))
		if _, err := NewCheckpointRepository(mock).FirstMismatch(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}
