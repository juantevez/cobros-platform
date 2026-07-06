package domain

import "testing"

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
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeName_idempotent(t *testing.T) {
	once := NormalizeName("  John   DOE ")
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
