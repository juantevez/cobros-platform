package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestNewIDs(t *testing.T) {
	gens := map[string]func() string{
		"endpoint": func() string { return NewEndpointID().String() },
		"delivery": func() string { return NewDeliveryID().String() },
		"attempt":  func() string { return NewAttemptID().String() },
	}
	for name, gen := range gens {
		a, b := gen(), gen()
		if _, err := uuid.Parse(a); err != nil || a == b {
			t.Errorf("%s ids should be unique uuids: %q %q (%v)", name, a, b, err)
		}
	}
}

func TestParseIDs(t *testing.T) {
	valid := "11111111-1111-1111-1111-111111111111"
	parsers := []struct {
		name    string
		parse   func(string) (string, error)
		errText string
	}{
		{"endpoint", func(s string) (string, error) { id, err := ParseEndpointID(s); return id.String(), err }, "invalid endpoint id"},
		{"delivery", func(s string) (string, error) { id, err := ParseDeliveryID(s); return id.String(), err }, "invalid delivery id"},
		{"tenant", func(s string) (string, error) { id, err := ParseTenantID(s); return id.String(), err }, "invalid tenant id"},
	}
	for _, p := range parsers {
		t.Run(p.name, func(t *testing.T) {
			if got, err := p.parse(valid); err != nil || got != valid {
				t.Fatalf("parse(valid) = %q, %v", got, err)
			}
			for _, in := range []string{"", "abc", "11111111-1111"} {
				got, err := p.parse(in)
				if err == nil || !strings.Contains(err.Error(), p.errText) || got != "" {
					t.Errorf("parse(%q) = %q, %v; want %q error", in, got, err, p.errText)
				}
				// El handler lo mapea a 400 por errors.Is.
				if !errors.Is(err, ErrInvalidID) {
					t.Errorf("parse(%q) error %v is not ErrInvalidID", in, err)
				}
			}
		})
	}
}

func TestDeliveryStatus(t *testing.T) {
	cases := []struct {
		status           DeliveryStatus
		str              string
		final, retryable bool
	}{
		{StatusPending, "pending", false, true},
		{StatusFailed, "failed", false, true},
		{StatusDelivered, "delivered", true, false},
		{StatusExhausted, "exhausted", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.str, func(t *testing.T) {
			if tc.status.String() != tc.str || tc.status.IsFinal() != tc.final || tc.status.IsRetryable() != tc.retryable {
				t.Errorf("String=%q IsFinal=%v IsRetryable=%v", tc.status.String(), tc.status.IsFinal(), tc.status.IsRetryable())
			}
		})
	}
}

// MaxAttempts se mantiene a mano; este test rompe si alguien cambia el
// schedule sin actualizarlo.
func TestMaxAttemptsMatchesRetrySchedule(t *testing.T) {
	if MaxAttempts != len(RetrySchedule)+1 {
		t.Fatalf("MaxAttempts = %d, want len(RetrySchedule)+1 = %d", MaxAttempts, len(RetrySchedule)+1)
	}
}

func TestNextRetryAt(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		attempts int
		want     time.Duration // 0 = nil
	}{
		{1, 30 * time.Second},
		{2, 2 * time.Minute},
		{3, 10 * time.Minute},
		{4, time.Hour},
		{5, 0},
		{6, 0},
		{0, 0},
		{-1, 0},
	}
	for _, tc := range cases {
		got := NextRetryAt(tc.attempts, now)
		if tc.want == 0 {
			if got != nil {
				t.Errorf("NextRetryAt(%d) = %v, want nil", tc.attempts, *got)
			}
			continue
		}
		if got == nil || !got.Equal(now.Add(tc.want)) {
			t.Errorf("NextRetryAt(%d) = %v, want now+%v", tc.attempts, got, tc.want)
		}
	}
}

func TestNewDeliveryAttempt(t *testing.T) {
	before := time.Now().UTC()
	a := NewDeliveryAttempt(2, 502, "bad gateway", "", 120)

	if _, err := uuid.Parse(a.ID().String()); err != nil {
		t.Errorf("attempt id is not a uuid: %v", err)
	}
	if a.AttemptNum() != 2 || a.HTTPStatus() != 502 || a.ResponseBody() != "bad gateway" || a.ErrMsg() != "" || a.DurationMs() != 120 {
		t.Errorf("fields not set: %+v", a)
	}
	if a.AttemptedAt().Before(before) || a.AttemptedAt().Location() != time.UTC {
		t.Errorf("attemptedAt = %v", a.AttemptedAt())
	}
}

func TestNewDeliveryAttempt_TruncatesBody(t *testing.T) {
	long := strings.Repeat("x", 501)
	if got := NewDeliveryAttempt(1, 500, long, "", 1).ResponseBody(); len(got) != 500 {
		t.Errorf("body len = %d, want 500", len(got))
	}
	exact := strings.Repeat("y", 500)
	if got := NewDeliveryAttempt(1, 500, exact, "", 1).ResponseBody(); got != exact {
		t.Error("500-char body should be kept intact")
	}
}

// Postgres rechaza UTF-8 inválido y NUL en TEXT: el corte a 500 bytes partía
// una "ñ" y el INSERT del intento fallaba (SQLSTATE 22021).
func TestNewDeliveryAttempt_StorableBody(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"cut does not split a multibyte rune", strings.Repeat("a", 499) + "ñ", strings.Repeat("a", 499)},
		{"rune ending exactly at the limit is kept", strings.Repeat("a", 498) + "ñ", strings.Repeat("a", 498) + "ñ"},
		{"invalid utf-8 is replaced", "ok\xff\xfe", "ok\uFFFD"},
		{"nul bytes are removed", "a\x00b", "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewDeliveryAttempt(1, 200, tc.body, "", 1).ResponseBody()
			if got != tc.want || !utf8.ValidString(got) || len(got) > 500 {
				t.Errorf("body = %q (len %d), want %q", got, len(got), tc.want)
			}
		})
	}
	if got := NewDeliveryAttempt(1, 0, "", "dial \xff\x00", 1).ErrMsg(); got != "dial \uFFFD" {
		t.Errorf("errMsg = %q", got)
	}
}

func TestDeliveryAttempt_Succeeded(t *testing.T) {
	for status, want := range map[int]bool{
		0: false, 199: false, 200: true, 204: true, 299: true, 300: false, 404: false, 500: false,
	} {
		if got := NewDeliveryAttempt(1, status, "", "", 1).Succeeded(); got != want {
			t.Errorf("Succeeded() with %d = %v, want %v", status, got, want)
		}
	}
}

func TestReconstituteAttempt(t *testing.T) {
	id := NewAttemptID()
	at := time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC)
	a := ReconstituteAttempt(id, 3, 0, "", "timeout", 5000, at)

	if a.ID() != id || a.AttemptNum() != 3 || a.HTTPStatus() != 0 || a.ErrMsg() != "timeout" || a.DurationMs() != 5000 || !a.AttemptedAt().Equal(at) {
		t.Errorf("attempt not restored: %+v", a)
	}
	// Body largo reconstituido no se trunca: se confía en lo persistido.
	if got := ReconstituteAttempt(id, 1, 200, strings.Repeat("z", 600), "", 1, at).ResponseBody(); len(got) != 600 {
		t.Errorf("reconstituted body len = %d", len(got))
	}
}
