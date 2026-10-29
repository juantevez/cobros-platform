package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

var screenCols = []string{"id", "full_name", "list_type", "country", "source", "normalized_name"}

var watchlistCols = []string{"id", "full_name", "list_type", "country", "source", "external_ref"}

func TestWatchlistRepository_Screen(t *testing.T) {
	t.Run("empty name short-circuits without query", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		matches, err := repo.Screen(context.Background(), "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matches != nil {
			t.Errorf("expected nil matches, got %+v", matches)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("no query should have run: %v", err)
		}
	})

	// expectScreenTx espera la tx efímera que fija el umbral de pg_trgm.
	expectScreenTx := func(mock pgxmock.PgxPoolIface) {
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("set_config").WithArgs("pg_trgm.word_similarity_threshold", "0.50").
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
	}

	t.Run("token + fuzzy prefilter on active entries, scored by domain", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		rows := pgxmock.NewRows(screenCols).
			AddRow("1", "Vladimir Petrov", "sanctions", "RU", "OFAC", "vladimir petrov").
			AddRow("2", "Petrov Vladimir", "pep", "RU", "EU", "petrov vladimir").
			AddRow("3", "Wladimir Petroff", "sanctions", "RU", "UN", "wladimir petroff").
			AddRow("4", "Ghost Holdings", "pep", "", "", "ghost holdings") // candidato difuso que el dominio descarta
		expectScreenTx(mock)
		mock.ExpectQuery(`FROM aml_watchlist\s+WHERE removed_at IS NULL AND \(string_to_array\(normalized_name, ' '\) <@ string_to_array\(\$1, ' '\) OR normalized_name %> \$2 OR normalized_name %> \$3 OR normalized_name %> \$4\)`).
			WithArgs("vladimir petrov holdings", "vladimir", "petrov", "holdings").WillReturnRows(rows)
		mock.ExpectCommit()

		matches, err := repo.Screen(context.Background(), "vladimir petrov holdings")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matches) != 3 {
			t.Fatalf("expected 3 matches, got %+v", matches)
		}
		want := []int{domain.ScoreOrderedMatch, domain.ScoreTokenMatch, domain.ScoreFuzzyMatch}
		for i, m := range matches {
			if m.Score != want[i] {
				t.Errorf("match %d (%s) score = %d, want %d", i, m.Entry.FullName, m.Score, want[i])
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("short tokens are not used for the fuzzy prefilter", func(t *testing.T) {
		mock := newMock(t)
		expectScreenTx(mock)
		mock.ExpectQuery(`<@ string_to_array\(\$1, ' '\)\)`).WithArgs("al co").
			WillReturnRows(pgxmock.NewRows(screenCols))
		mock.ExpectCommit()
		if _, err := NewWatchlistRepository(mock).Screen(context.Background(), "al co"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectScreenTx(mock)
		mock.ExpectQuery("FROM aml_watchlist").WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		if _, err := NewWatchlistRepository(mock).Screen(context.Background(), "xxxx"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		mock := newMock(t)
		expectScreenTx(mock)
		bad := pgxmock.NewRows([]string{"id", "full_name"}).AddRow("1", "x") // menos columnas
		mock.ExpectQuery("FROM aml_watchlist").WillReturnRows(bad)
		mock.ExpectRollback()
		if _, err := NewWatchlistRepository(mock).Screen(context.Background(), "xxxx"); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

func TestWatchlistRepository_Add(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	entry := domain.WatchlistEntry{
		ID: "wl-1", FullName: "Juan Perez", ListType: "pep", Country: "AR", Source: "local",
	}

	t.Run("inserts entry", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectExec("INSERT INTO aml_watchlist").
			WithArgs("wl-1", "Juan Perez", "juan perez", "pep", "AR", "local", pgxmock.AnyArg(), now).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.Add(context.Background(), entry, "juan perez", now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectExec("INSERT INTO aml_watchlist").WillReturnError(errors.New("boom"))
		if err := repo.Add(context.Background(), entry, "juan perez", now); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestWatchlistRepository_Remove(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("soft deletes active entry", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectExec("UPDATE aml_watchlist SET removed_at=\\$2\\s+WHERE id=\\$1 AND removed_at IS NULL").
			WithArgs("wl-1", now).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		if err := repo.Remove(context.Background(), "wl-1", now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("expectations: %v", err)
		}
	})

	t.Run("missing or already removed maps to ErrWatchlistEntryNotFound", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectExec("UPDATE aml_watchlist").WithArgs("wl-1", now).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		if err := repo.Remove(context.Background(), "wl-1", now); !errors.Is(err, domain.ErrWatchlistEntryNotFound) {
			t.Fatalf("expected ErrWatchlistEntryNotFound, got %v", err)
		}
	})

	t.Run("db error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectExec("UPDATE aml_watchlist").WillReturnError(errors.New("boom"))
		if err := repo.Remove(context.Background(), "wl-1", now); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestWatchlistRepository_List(t *testing.T) {
	t.Run("returns entries", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		rows := pgxmock.NewRows(watchlistCols).
			AddRow("1", "Alice", "sanctions", "US", "OFAC", "").
			AddRow("2", "Bob", "pep", "UK", "EU", "ref-2")
		mock.ExpectQuery("FROM aml_watchlist WHERE removed_at IS NULL\\s+ORDER BY full_name LIMIT \\$1").WithArgs(100).WillReturnRows(rows)

		out, err := repo.List(context.Background(), 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 || out[0].FullName != "Alice" || out[1].Source != "EU" {
			t.Errorf("unexpected entries: %+v", out)
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectQuery("FROM aml_watchlist").WithArgs(100).WillReturnError(errors.New("boom"))
		if _, err := repo.List(context.Background(), 100); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		bad := pgxmock.NewRows([]string{"id", "full_name"}).AddRow("1", "x")
		mock.ExpectQuery("FROM aml_watchlist").WithArgs(100).WillReturnRows(bad)
		if _, err := repo.List(context.Background(), 100); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

func TestWatchlistRepository_ListActiveBySource(t *testing.T) {
	mock := newMock(t)
	repo := NewWatchlistRepository(mock)
	rows := pgxmock.NewRows(watchlistCols).AddRow("1", "Vladimir Petrov", "sanctions", "RU", "OFAC", "SDN-36")
	mock.ExpectQuery(`WHERE removed_at IS NULL AND source = \$1 AND external_ref IS NOT NULL`).
		WithArgs("OFAC").WillReturnRows(rows)

	out, err := repo.ListActiveBySource(context.Background(), "OFAC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0].ExternalRef != "SDN-36" || out[0].Source != "OFAC" {
		t.Errorf("unexpected entries: %+v", out)
	}
}

func TestWatchlistRepository_AddPersistsExternalRef(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	mock := newMock(t)
	ref := "SDN-36"
	mock.ExpectExec("INSERT INTO aml_watchlist .*external_ref").
		WithArgs("wl-1", "Vladimir Petrov", "vladimir petrov", "sanctions", "RU", "OFAC", &ref, now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	entry := domain.WatchlistEntry{ID: "wl-1", FullName: "Vladimir Petrov", ListType: "sanctions",
		Country: "RU", Source: "OFAC", ExternalRef: ref}
	if err := NewWatchlistRepository(mock).Add(context.Background(), entry, "vladimir petrov", now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
