package eventbus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// fakeMsg implementa lo que process usa de jetstream.Msg y registra el
// desenlace (ack, nak o term).
type fakeMsg struct {
	jetstream.Msg
	delivered uint64
	outcome   string
}

func (m *fakeMsg) Subject() string                  { return "ledger.test.v1" }
func (m *fakeMsg) Data() []byte                     { return []byte(`{}`) }
func (m *fakeMsg) Headers() nats.Header             { return nil }
func (m *fakeMsg) Ack() error                       { m.outcome = "ack"; return nil }
func (m *fakeMsg) Term() error                      { m.outcome = "term"; return nil }
func (m *fakeMsg) NakWithDelay(time.Duration) error { m.outcome = "nak"; return nil }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: m.delivered, Sequence: jetstream.SequencePair{Stream: 42}}, nil
}

func newTestConsumer() (*NatsConsumer, *bytes.Buffer) {
	var logs bytes.Buffer
	return &NatsConsumer{logger: slog.New(slog.NewTextHandler(&logs, nil))}, &logs
}

func TestProcess_Outcomes(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name      string
		err       error
		delivered uint64
		outcome   string
		log       string
	}{
		{"success acks", nil, 1, "ack", ""},
		{"transient error naks", boom, 1, "nak", "sending nak"},
		{"permanent error terminates", Permanent(boom), 1, "term", "permanent handler error"},
		{"wrapped permanent terminates", fmt.Errorf("ctx: %w", Permanent(boom)), 1, "term", "permanent handler error"},
		{"last delivery is logged as discarded", boom, 5, "nak", "max deliveries reached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, logs := newTestConsumer()
			msg := &fakeMsg{delivered: tc.delivered}
			c.process(context.Background(), msg, func(context.Context, *Message) error { return tc.err }, 5)
			if msg.outcome != tc.outcome {
				t.Errorf("outcome = %q, want %q", msg.outcome, tc.outcome)
			}
			if tc.log != "" && !strings.Contains(logs.String(), tc.log) {
				t.Errorf("log %q missing in %q", tc.log, logs.String())
			}
			if tc.log != "" && !strings.Contains(logs.String(), "streamSeq=42") {
				t.Errorf("log must carry the stream sequence: %q", logs.String())
			}
		})
	}
}

func TestPermanent(t *testing.T) {
	if Permanent(nil) != nil {
		t.Error("Permanent(nil) must be nil")
	}
	boom := errors.New("boom")
	if !errors.Is(Permanent(boom), boom) {
		t.Error("Permanent must wrap the original error")
	}
	if IsPermanent(boom) {
		t.Error("plain error is not permanent")
	}
}
