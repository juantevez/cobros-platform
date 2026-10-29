package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

func feedRows() []WatchlistFeedRow {
	return []WatchlistFeedRow{
		{Line: 2, ExternalRef: "1", FullName: "Vladimir Petrov", ListType: "sanctions", Country: "RU"},
		{Line: 3, ExternalRef: "2", FullName: "Ivan Sokolov", ListType: "SANCTIONS", Country: "by"},
		{Line: 4, ExternalRef: "4", FullName: "Kim Jong Trading", ListType: "sanctions", Country: "KP"},
	}
}

func activeEntries() []domain.WatchlistEntry {
	return []domain.WatchlistEntry{
		{ID: "id-1", ExternalRef: "1", FullName: "Vladimir Petrov", ListType: "sanctions", Country: "RU", Source: "OFAC"},
		{ID: "id-2", ExternalRef: "2", FullName: "Ivan Sokolov", ListType: "sanctions", Country: "RU", Source: "OFAC"},
		{ID: "id-3", ExternalRef: "3", FullName: "Global Shell", ListType: "pep", Country: "PA", Source: "OFAC"},
	}
}

func TestImportWatchlistUseCase(t *testing.T) {
	newUC := func(w *fakeWatchlist) *ImportWatchlistUseCase {
		return NewImportWatchlistUseCase(w, fakeTx{}, newClock())
	}

	t.Run("applies the delta for the source", func(t *testing.T) {
		w := &fakeWatchlist{active: activeEntries()}
		res, err := newUC(w).Execute(context.Background(), ImportWatchlistCmd{Source: " ofac ", Rows: feedRows()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImportWatchlistResult{Source: "OFAC", Added: 1, Updated: 1, Removed: 1, Unchanged: 1}
		if res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
		if w.gotSource != "OFAC" {
			t.Errorf("source = %q", w.gotSource)
		}
		// Baja: la que ya no viene (id-3) + la versión vieja de la actualizada (id-2).
		if strings.Join(w.removed, ",") != "id-3,id-2" {
			t.Errorf("removed = %v", w.removed)
		}
		if len(w.added) != 2 {
			t.Fatalf("added = %+v", w.added)
		}
		for _, e := range w.added {
			if e.Source != "OFAC" || e.ExternalRef == "" || e.ID == "" {
				t.Errorf("added entry incomplete: %+v", e)
			}
		}
	})

	t.Run("dry run computes without writing", func(t *testing.T) {
		w := &fakeWatchlist{active: activeEntries()}
		res, err := newUC(w).Execute(context.Background(), ImportWatchlistCmd{Source: "OFAC", Rows: feedRows(), DryRun: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.DryRun || res.Added != 1 || res.Removed != 1 {
			t.Errorf("result = %+v", res)
		}
		if len(w.added) != 0 || len(w.removed) != 0 {
			t.Error("dry run must not write")
		}
	})

	t.Run("invalid feed is rejected as a whole with line numbers", func(t *testing.T) {
		w := &fakeWatchlist{active: activeEntries()}
		rows := []WatchlistFeedRow{
			{Line: 2, ExternalRef: "1", FullName: "Ok Name", ListType: "sanctions"},
			{Line: 3, ExternalRef: "", FullName: "Sin ref", ListType: "pep"},
			{Line: 4, ExternalRef: "1", FullName: "Duplicada", ListType: "pep"},
			{Line: 5, ExternalRef: "9", FullName: "Tipo raro", ListType: "terror"},
			{Line: 6, ExternalRef: "8", FullName: " -- ", ListType: "pep"},
		}
		_, err := newUC(w).Execute(context.Background(), ImportWatchlistCmd{Source: "OFAC", Rows: rows})
		var fe *FeedError
		if !errors.As(err, &fe) || !errors.Is(err, domain.ErrInvalidWatchlistFeed) {
			t.Fatalf("expected FeedError, got %v", err)
		}
		if len(fe.Problems) != 4 {
			t.Errorf("problems = %v", fe.Problems)
		}
		for _, line := range []string{"line 3", "line 4", "line 5", "line 6"} {
			if !strings.Contains(err.Error(), line) {
				t.Errorf("missing %q in %v", line, err)
			}
		}
		if len(w.added)+len(w.removed) != 0 || w.gotSource != "" {
			t.Error("nothing should be read or written for an invalid feed")
		}
	})

	t.Run("empty feed or missing source rejected", func(t *testing.T) {
		uc := newUC(&fakeWatchlist{})
		if _, err := uc.Execute(context.Background(), ImportWatchlistCmd{Source: "OFAC"}); !errors.Is(err, domain.ErrInvalidWatchlistFeed) {
			t.Errorf("empty feed: %v", err)
		}
		if _, err := uc.Execute(context.Background(), ImportWatchlistCmd{Rows: feedRows()}); !errors.Is(err, domain.ErrInvalidWatchlistFeed) {
			t.Errorf("missing source: %v", err)
		}
	})

	t.Run("repo errors propagated", func(t *testing.T) {
		if _, err := newUC(&fakeWatchlist{listErr: errBoom}).Execute(context.Background(),
			ImportWatchlistCmd{Source: "OFAC", Rows: feedRows()}); !errors.Is(err, errBoom) {
			t.Errorf("list: %v", err)
		}
		if _, err := newUC(&fakeWatchlist{active: activeEntries(), addErr: errBoom}).Execute(context.Background(),
			ImportWatchlistCmd{Source: "OFAC", Rows: feedRows()}); !errors.Is(err, errBoom) {
			t.Errorf("add: %v", err)
		}
	})
}
