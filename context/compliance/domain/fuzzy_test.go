package domain

import "testing"

func TestMatchScore_fuzzy(t *testing.T) {
	cases := []struct {
		name, entry, subject string
		want                 int
	}{
		{"transliteration ov/off", "vladimir petrov", "wladimir petroff holdings s a", ScoreFuzzyMatch},
		{"typo one edit", "vladimir petrov", "vladimir petrof", ScoreFuzzyMatch},
		{"arabic transliteration", "muhammad al rashid", "mohamed al rashid trading", ScoreFuzzyMatch},
		{"spanish z/s", "gonzalez martinez", "gonsales martines hnos", ScoreFuzzyMatch},
		{"typo long token two edits", "kim jong trading co", "kim jong traiding co", ScoreFuzzyMatch},
		{"transposition", "ivan sokolov", "ivan sokolvo", ScoreFuzzyMatch},
		{"exact still wins", "vladimir petrov", "vladimir petrov", ScoreExactMatch},
		{"reordered still wins", "vladimir petrov", "petrov vladimir", ScoreTokenMatch},

		{"unrelated", "vladimir petrov", "juan perez srl", 0},
		{"shared word only", "global shell holdings", "panaderia la global srl", 0},
		{"single-token entries never fuzzy", "hamas", "hamaz", 0},
		{"short tokens must be exact", "al bin", "el ben", 0},
		{"too different", "ivan sokolov", "ivan sokolovsky", 0},
		{"not enough name tokens", "kim jong trading co", "kim jong trading", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchScore(c.entry, c.subject); got != c.want {
				t.Errorf("MatchScore(%q, %q) = %d, want %d", c.entry, c.subject, got, c.want)
			}
		})
	}
	if RiskFromScore(ScoreFuzzyMatch) != RiskMedium {
		t.Error("fuzzy matches must be medium risk")
	}
}

func TestPhoneticKey(t *testing.T) {
	same := [][2]string{
		{"petrov", "petroff"},
		{"muhammad", "mohamed"},
		{"gonzalez", "gonsales"},
		{"vladimir", "wladimir"},
		{"philip", "filip"},
		{"cesar", "sesar"},
	}
	for _, p := range same {
		if phoneticKey(p[0]) != phoneticKey(p[1]) {
			t.Errorf("phoneticKey(%q)=%q != phoneticKey(%q)=%q", p[0], phoneticKey(p[0]), p[1], phoneticKey(p[1]))
		}
	}
	if phoneticKey("petrov") == phoneticKey("perez") {
		t.Error("unrelated names should differ")
	}
}

func TestDamerauLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"abc", "abc", 0}, {"abc", "abd", 1}, {"abc", "ab", 1},
		{"abc", "acb", 1}, {"sokolov", "sokolvo", 1}, {"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := damerauLevenshtein([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("DL(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
