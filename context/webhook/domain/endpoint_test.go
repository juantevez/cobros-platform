package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testTenant = TenantID("11111111-1111-1111-1111-111111111111")

func newTestEndpoint(t *testing.T, events ...string) *WebhookEndpoint {
	t.Helper()
	if len(events) == 0 {
		events = []string{"payment.captured"}
	}
	e, err := NewWebhookEndpoint(NewEndpointID(), testTenant, "https://merchant.example.com/hooks", "whsec_0123456789abcdef", events, "main")
	if err != nil {
		t.Fatalf("NewWebhookEndpoint: %v", err)
	}
	return e
}

func TestNewWebhookEndpoint(t *testing.T) {
	t.Run("creates active endpoint and records registered event", func(t *testing.T) {
		id := NewEndpointID()
		before := time.Now().UTC()

		e, err := NewWebhookEndpoint(id, testTenant, "https://merchant.example.com/hooks", "whsec_0123456789abcdef",
			[]string{"payment.captured.v1", " payout.confirmed ", "payment.captured", ""}, "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.ID() != id || e.TenantID() != testTenant || e.URL() != "https://merchant.example.com/hooks" || e.Description() != "prod" {
			t.Error("fields not set")
		}
		if e.Secret() != "whsec_0123456789abcdef" || e.SecretHint() != "...cdef" {
			t.Errorf("secret=%q hint=%q", e.Secret(), e.SecretHint())
		}
		if want := []string{"payment.captured", "payout.confirmed"}; !reflect.DeepEqual(e.Events(), want) {
			t.Errorf("events = %v, want %v", e.Events(), want)
		}
		if !e.Active() {
			t.Error("new endpoint should be active")
		}
		if e.CreatedAt().Before(before) || !e.CreatedAt().Equal(e.UpdatedAt()) || e.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt=%v updatedAt=%v", e.CreatedAt(), e.UpdatedAt())
		}

		evs := e.PullEvents()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		ev, ok := evs[0].(EndpointRegisteredEvent)
		if !ok {
			t.Fatalf("expected EndpointRegisteredEvent, got %T", evs[0])
		}
		if ev.EndpointID != id.String() || ev.TenantID != testTenant.String() || ev.URL != e.URL() ||
			!reflect.DeepEqual(ev.Events, e.Events()) || ev.EventTenantID() != testTenant.String() {
			t.Errorf("event = %+v", ev)
		}
		if len(e.PullEvents()) != 0 {
			t.Error("second pull should be empty")
		}
	})

	hints := []struct{ secret, want string }{
		{"abcdefgh", "...efgh"},
		{"abcde", "...bcde"},
		{"abcd", "abcd"}, // ≤ 4 chars: el hint es el secret completo
		{"", ""},
	}
	for _, tc := range hints {
		t.Run("secret hint for "+tc.secret, func(t *testing.T) {
			e, err := NewWebhookEndpoint(NewEndpointID(), testTenant, "https://x.example.com", tc.secret, []string{"a.b"}, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if e.SecretHint() != tc.want {
				t.Errorf("hint = %q, want %q", e.SecretHint(), tc.want)
			}
		})
	}

	invalid := []struct {
		name    string
		url     string
		events  []string
		wantErr error
		wantMsg string
	}{
		{"empty url", "", []string{"a.b"}, ErrEndpointURLEmpty, ""},
		{"relative url", "merchant.example.com/hooks", []string{"a.b"}, ErrInvalidEndpointURL, ""},
		{"path without host", "/hooks", []string{"a.b"}, ErrInvalidEndpointURL, "scheme must be http or https"},
		{"non http scheme", "ftp://merchant.example.com/hooks", []string{"a.b"}, ErrInvalidEndpointURL, "scheme must be http or https"},
		{"scheme without host", "https://", []string{"a.b"}, ErrInvalidEndpointURL, "missing host"},
		{"no events", "https://x.example.com", nil, ErrNoEventsSubscribed, ""},
		{"empty events slice", "https://x.example.com", []string{}, ErrNoEventsSubscribed, ""},
		{"only blank events", "https://x.example.com", []string{"", "  "}, ErrNoEventsSubscribed, ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewWebhookEndpoint(NewEndpointID(), testTenant, tc.url, "secret", tc.events, "")
			if e != nil {
				t.Error("endpoint should be nil on error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			if tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
		})
	}
}

func TestNewWebhookEndpoint_AcceptedURLs(t *testing.T) {
	for _, u := range []string{"https://merchant.example.com/hooks", "http://localhost:8080/hooks", "https://merchant.example.com:8443/a?b=c"} {
		if _, err := NewWebhookEndpoint(NewEndpointID(), testTenant, u, "secret", []string{"a.b"}, ""); err != nil {
			t.Errorf("%q should be accepted: %v", u, err)
		}
	}
}

func TestWebhookEndpoint_Deactivate(t *testing.T) {
	e := newTestEndpoint(t)
	e.PullEvents()
	created := e.UpdatedAt()

	e.Deactivate()

	if e.Active() {
		t.Error("endpoint should be inactive")
	}
	if e.UpdatedAt().Before(created) {
		t.Error("updatedAt should advance")
	}
	evs := e.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev, ok := evs[0].(EndpointDeactivatedEvent)
	if !ok || ev.EndpointID != e.ID().String() || ev.TenantID != testTenant.String() {
		t.Errorf("event = %#v", evs[0])
	}
}

func TestWebhookEndpoint_SubscribesTo(t *testing.T) {
	e := newTestEndpoint(t, "payment.captured", "payout.confirmed")

	cases := map[string]bool{
		"payment.captured":    true,
		"payment.captured.v1": true,
		"payment.captured.v2": true, // cualquier versión del mismo tipo
		"payout.confirmed.v1": true,
		"payment.failed.v1":   false,
		"payment":             false,
		"":                    false,
	}
	for eventType, want := range cases {
		if got := e.SubscribesTo(eventType); got != want {
			t.Errorf("SubscribesTo(%q) = %v, want %v", eventType, got, want)
		}
	}
}

func TestWebhookEndpoint_ComputeSignature(t *testing.T) {
	// Vector calculado aparte, como lo haría el comercio:
	//   hmac.new(b"whsec_test", b'1700000000.{"a":1}', sha256).hexdigest()
	e := ReconstituteWebhookEndpoint(NewEndpointID(), testTenant, "https://x.example.com", "whsec_test", "", "", []string{"a.b"}, true, time.Now(), time.Now())
	got := e.ComputeSignature(1700000000, []byte(`{"a":1}`))
	want := "38877139021993b830af32feea6e18a8da83eb2f6e49ee50bd9e4cf4ca4d3789"
	if got != want {
		t.Errorf("signature = %s, want %s", got, want)
	}
	if h := e.SignatureHeader(1700000000, []byte(`{"a":1}`)); h != "t=1700000000,v1="+want {
		t.Errorf("header = %s", h)
	}

	if e.ComputeSignature(1, []byte("a")) == e.ComputeSignature(1, []byte("b")) {
		t.Error("different payloads must produce different signatures")
	}
	// El timestamp está firmado: el mismo body con otro t no verifica (replay).
	if e.ComputeSignature(1, []byte("a")) == e.ComputeSignature(2, []byte("a")) {
		t.Error("different timestamps must produce different signatures")
	}
	other := ReconstituteWebhookEndpoint(NewEndpointID(), testTenant, "https://x.example.com", "other", "", "", []string{"a.b"}, true, time.Now(), time.Now())
	if other.ComputeSignature(1, []byte("a")) == e.ComputeSignature(1, []byte("a")) {
		t.Error("different secrets must produce different signatures")
	}
}

func TestReconstituteWebhookEndpoint(t *testing.T) {
	id := NewEndpointID()
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)

	e := ReconstituteWebhookEndpoint(id, testTenant, "https://x.example.com", "sec", "hint", "desc",
		[]string{"payment.captured"}, false, created, updated)

	if e.ID() != id || e.TenantID() != testTenant || e.URL() != "https://x.example.com" || e.Secret() != "sec" ||
		e.SecretHint() != "hint" || e.Description() != "desc" || e.Active() {
		t.Error("fields not restored")
	}
	if !reflect.DeepEqual(e.Events(), []string{"payment.captured"}) || !e.CreatedAt().Equal(created) || !e.UpdatedAt().Equal(updated) {
		t.Error("events/timestamps not restored")
	}
	if len(e.PullEvents()) != 0 {
		t.Error("reconstitution must not emit events")
	}
}

func TestStripVersion(t *testing.T) {
	cases := map[string]string{
		"payment.captured.v1":  "payment.captured",
		"payment.captured.v12": "payment.captured",
		"payment.captured":     "payment.captured",
		"payment.captured.v":   "payment.captured.v",   // sin dígitos
		"payment.captured.vX1": "payment.captured.vX1", // no todo dígitos
		"payment.captured.V1":  "payment.captured.V1",  // case-sensitive
		"payment.v1.captured":  "payment.v1.captured",  // sólo el último segmento
		"":                     "",
	}
	for in, want := range cases {
		if got := stripVersion(in); got != want {
			t.Errorf("stripVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeEvents(t *testing.T) {
	got := normalizeEvents([]string{"a.b.v1", "a.b", " c.d ", "", "  ", "c.d.v2"})
	if want := []string{"a.b", "c.d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := normalizeEvents(nil); got == nil || len(got) != 0 {
		t.Errorf("nil input should give empty non-nil slice, got %#v", got)
	}
}
