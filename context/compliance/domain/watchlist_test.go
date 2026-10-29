package domain

import (
	"errors"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Juan Perez", "juan perez"},
		{"  JUAN   PEREZ  ", "juan perez"},
		{"osama bin laden", "osama bin laden"},
		{"\tMaria\nLopez ", "maria lopez"},
		{"SingleName", "singlename"},
		{"", ""},
		{"   ", ""},
		{"José Pérez", "jose perez"},
		{"MÜLLER  Straße", "muller straße"},
		{"Pérez-Gómez, S.A.", "perez gomez s a"},
		{"O'Brien & Co.", "o brien co"},
		{"Kim Jong Trading Co.", "kim jong trading co"},
		{"---", ""},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeName_idempotent(t *testing.T) {
	once := NormalizeName("  Jöhn   DOE, S.A. ")
	twice := NormalizeName(once)
	if once != twice {
		t.Errorf("not idempotent: %q vs %q", once, twice)
	}
}

func TestRiskFromScore(t *testing.T) {
	cases := []struct {
		score int
		want  RiskLevel
	}{
		{100, RiskHigh},
		{90, RiskHigh},   // límite inferior high
		{89, RiskMedium}, // justo por debajo
		{60, RiskMedium}, // límite inferior medium
		{59, RiskLow},    // justo por debajo
		{0, RiskLow},
		{-5, RiskLow}, // score negativo defensivo
	}
	for _, c := range cases {
		if got := RiskFromScore(c.score); got != c.want {
			t.Errorf("RiskFromScore(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestMatchScore(t *testing.T) {
	cases := []struct {
		name, entry, subject string
		want                 int
	}{
		{"exact", "vladimir petrov", "vladimir petrov", ScoreExactMatch},
		{"contiguous within longer name", "vladimir petrov", "vladimir petrov holdings s a", ScoreOrderedMatch},
		{"reordered tokens", "vladimir petrov", "petrov vladimir", ScoreTokenMatch},
		{"interleaved middle name", "vladimir petrov", "vladimir ivanovich petrov", ScoreTokenMatch},
		{"substring of a token is not a match", "ivan sokolov", "ivan sokolovsky", 0},
		{"casual substring is not a match", "ivan", "ivanka trading", 0},
		{"missing token", "kim jong trading co", "kim jong trading", 0},
		{"empty entry", "", "anything", 0},
		{"empty subject", "x", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchScore(c.entry, c.subject); got != c.want {
				t.Errorf("MatchScore(%q, %q) = %d, want %d", c.entry, c.subject, got, c.want)
			}
		})
	}
}

func TestMatchScore_accentInsensitiveViaNormalize(t *testing.T) {
	if got := MatchScore(NormalizeName("José Pérez"), NormalizeName("JOSE PEREZ SRL")); got != ScoreOrderedMatch {
		t.Errorf("score = %d, want %d", got, ScoreOrderedMatch)
	}
}

func TestNewWatchlistEntry(t *testing.T) {
	t.Run("valid entry is trimmed", func(t *testing.T) {
		e, err := NewWatchlistEntry("id-1", "  Vladimir Petrov ", ListTypeSanctions, " ru ", " OFAC ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.FullName != "Vladimir Petrov" || e.Country != "RU" || e.Source != "OFAC" || e.ID != "id-1" {
			t.Errorf("unexpected entry: %+v", e)
		}
	})

	t.Run("pep accepted", func(t *testing.T) {
		if _, err := NewWatchlistEntry("id", "X", ListTypePEP, "", ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid list type", func(t *testing.T) {
		_, err := NewWatchlistEntry("id", "X", "terrorist", "", "")
		if !errors.Is(err, ErrInvalidListType) {
			t.Fatalf("expected ErrInvalidListType, got %v", err)
		}
	})

	t.Run("name without letters or digits", func(t *testing.T) {
		_, err := NewWatchlistEntry("id", " -- ", ListTypePEP, "", "")
		if !errors.Is(err, ErrInvalidWatchlistName) {
			t.Fatalf("expected ErrInvalidWatchlistName, got %v", err)
		}
	})
}

func TestPlanWatchlistSync(t *testing.T) {
	e := func(ref, name, list, country string) WatchlistEntry {
		return WatchlistEntry{ID: "id-" + ref, ExternalRef: ref, FullName: name, ListType: list, Country: country, Source: "OFAC"}
	}
	current := []WatchlistEntry{
		e("1", "Vladimir Petrov", "sanctions", "RU"),
		e("2", "Ivan Sokolov", "sanctions", "RU"),
		e("3", "Global Shell", "pep", "PA"),
	}
	incoming := []WatchlistEntry{
		e("1", "Vladimir Petrov", "sanctions", "RU"),  // sin cambios
		e("2", "Ivan Sokolov", "sanctions", "BY"),     // cambió el país
		e("4", "Kim Jong Trading", "sanctions", "KP"), // nueva
	}

	plan := PlanWatchlistSync(current, incoming)
	if plan.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1", plan.Unchanged)
	}
	if len(plan.ToAdd) != 1 || plan.ToAdd[0].ExternalRef != "4" {
		t.Errorf("toAdd = %+v", plan.ToAdd)
	}
	if len(plan.ToReplace) != 1 || plan.ToReplace[0].Old.ID != "id-2" || plan.ToReplace[0].New.Country != "BY" {
		t.Errorf("toReplace = %+v", plan.ToReplace)
	}
	if len(plan.ToRemove) != 1 || plan.ToRemove[0].ExternalRef != "3" {
		t.Errorf("toRemove = %+v", plan.ToRemove)
	}

	t.Run("identical feed is a no-op", func(t *testing.T) {
		p := PlanWatchlistSync(current, current)
		if p.Unchanged != 3 || len(p.ToAdd)+len(p.ToReplace)+len(p.ToRemove) != 0 {
			t.Errorf("unexpected plan: %+v", p)
		}
	})
}
