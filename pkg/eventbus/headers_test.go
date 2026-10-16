package eventbus

import (
	"reflect"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestToNatsMsg(t *testing.T) {
	m := toNatsMsg(Message{
		Subject: "payment.captured.v1",
		ID:      "evt-1",
		Payload: []byte(`{"a":1}`),
		Headers: map[string]string{"occurred-at": "2026-10-16T12:00:00Z", "correlation-id": "c-1"},
	})

	if m.Subject != "payment.captured.v1" || string(m.Data) != `{"a":1}` {
		t.Errorf("subject/data = %q %q", m.Subject, m.Data)
	}
	// Los headers NATS son case-sensitive: las claves viajan tal cual.
	if m.Header.Get("occurred-at") != "2026-10-16T12:00:00Z" || m.Header.Get("correlation-id") != "c-1" {
		t.Errorf("headers = %v", m.Header)
	}
}

func TestToNatsMsg_NoHeaders(t *testing.T) {
	m := toNatsMsg(Message{Subject: "s", Payload: []byte("x")})
	if len(m.Header) != 0 {
		t.Errorf("headers = %v, want empty", m.Header)
	}
}

func TestHeadersFrom(t *testing.T) {
	cases := []struct {
		name string
		in   nats.Header
		want map[string]string
	}{
		{"nil", nil, nil},
		{"empty", nats.Header{}, nil},
		{"first value of each key", nats.Header{
			"occurred-at": {"2026-10-16T12:00:00Z"},
			"Nats-Msg-Id": {"evt-1"},
			"multi":       {"a", "b"},
			"empty":       {},
		}, map[string]string{
			"occurred-at": "2026-10-16T12:00:00Z",
			"Nats-Msg-Id": "evt-1",
			"multi":       "a",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := headersFrom(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHeadersRoundTrip(t *testing.T) {
	in := map[string]string{"occurred-at": "2026-10-16T12:00:00.123456789Z", "content-type": "application/json"}
	if got := headersFrom(toNatsMsg(Message{Subject: "s", Headers: in}).Header); !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %v, want %v", got, in)
	}
}
