package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

var watchlistCols = []string{"id", "full_name", "list_type", "country", "source"}

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

	t.Run("returns matches with score 90", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		rows := pgxmock.NewRows(watchlistCols).
			AddRow("1", "Vladimir Petrov", "sanctions", "RU", "OFAC").
			AddRow("2", "Ivan Ivanov", "pep", "RU", "EU")
		mock.ExpectQuery("FROM aml_watchlist").WithArgs("vladimir petrov holdings").WillReturnRows(rows)

		matches, err := repo.Screen(context.Background(), "vladimir petrov holdings")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(matches))
		}
		if matches[0].Score != 90 || matches[0].Entry.FullName != "Vladimir Petrov" {
			t.Errorf("unexpected match: %+v", matches[0])
		}
	})

	t.Run("query error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		mock.ExpectQuery("FROM aml_watchlist").WithArgs("x").WillReturnError(errors.New("boom"))
		if _, err := repo.Screen(context.Background(), "x"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		bad := pgxmock.NewRows([]string{"id", "full_name"}).AddRow("1", "x") // menos columnas
		mock.ExpectQuery("FROM aml_watchlist").WithArgs("x").WillReturnRows(bad)
		if _, err := repo.Screen(context.Background(), "x"); err == nil {
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
			WithArgs("wl-1", "Juan Perez", "juan perez", "pep", "AR", "local", now).
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

func TestWatchlistRepository_List(t *testing.T) {
	t.Run("returns entries", func(t *testing.T) {
		mock := newMock(t)
		repo := NewWatchlistRepository(mock)
		rows := pgxmock.NewRows(watchlistCols).
			AddRow("1", "Alice", "sanctions", "US", "OFAC").
			AddRow("2", "Bob", "pep", "UK", "EU")
		mock.ExpectQuery("FROM aml_watchlist ORDER BY full_name LIMIT \\$1").WithArgs(100).WillReturnRows(rows)

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
