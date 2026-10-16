package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

const testTenant = domain.TenantID("11111111-1111-1111-1111-111111111111")

func endpointFor(url string) *domain.WebhookEndpoint {
	return domain.ReconstituteWebhookEndpoint(domain.NewEndpointID(), testTenant, url,
		"whsec_test", "...test", "", []string{"payment.captured"}, true, time.Now(), time.Now())
}

func deliveryWithAttempts(n int) *domain.WebhookDelivery {
	now := time.Now().UTC()
	return domain.ReconstituteDelivery(domain.NewDeliveryID(), domain.NewEndpointID(), testTenant,
		"payment.captured", "evt-1", json.RawMessage(`{"event_type":"payment.captured.v1"}`),
		domain.StatusFailed, n, &now, nil, now, now)
}

func TestNewDispatcher_Timeout(t *testing.T) {
	if got := NewDispatcher().client.Timeout; got != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", got)
	}
}

func TestDispatcher_Dispatch(t *testing.T) {
	t.Run("posts signed payload and records response", func(t *testing.T) {
		var gotReq *http.Request
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotReq = r
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("thanks"))
		}))
		defer srv.Close()

		e := endpointFor(srv.URL + "/hooks")
		d := deliveryWithAttempts(2)
		before := time.Now().Unix()

		attempt, err := NewDispatcher().Dispatch(context.Background(), e, d)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if gotReq.Method != http.MethodPost || gotReq.URL.Path != "/hooks" {
			t.Errorf("request = %s %s", gotReq.Method, gotReq.URL.Path)
		}
		if string(gotBody) != string(d.Payload()) {
			t.Errorf("body = %s, want %s", gotBody, d.Payload())
		}
		headers := map[string]string{
			"Content-Type":       "application/json",
			"X-Cobros-Signature": "sha256=" + e.ComputeSignature(d.Payload()),
			"X-Cobros-Event":     "payment.captured",
			"X-Cobros-Delivery":  d.ID().String(),
			"User-Agent":         "cobros-platform-webhooks/1.0",
		}
		for k, want := range headers {
			if got := gotReq.Header.Get(k); got != want {
				t.Errorf("header %s = %q, want %q", k, got, want)
			}
		}
		ts, err := strconv.ParseInt(gotReq.Header.Get("X-Cobros-Timestamp"), 10, 64)
		if err != nil || ts < before || ts > time.Now().Unix() {
			t.Errorf("timestamp header = %q", gotReq.Header.Get("X-Cobros-Timestamp"))
		}

		if attempt.AttemptNum() != 3 || attempt.HTTPStatus() != 201 || attempt.ResponseBody() != "thanks" || attempt.ErrMsg() != "" {
			t.Errorf("attempt = num %d status %d body %q err %q", attempt.AttemptNum(), attempt.HTTPStatus(), attempt.ResponseBody(), attempt.ErrMsg())
		}
		if !attempt.Succeeded() || attempt.DurationMs() < 0 {
			t.Errorf("succeeded=%v duration=%d", attempt.Succeeded(), attempt.DurationMs())
		}
	})

	t.Run("non-2xx response is a failed attempt, not an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		attempt, err := NewDispatcher().Dispatch(context.Background(), endpointFor(srv.URL), deliveryWithAttempts(0))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempt.HTTPStatus() != 503 || attempt.Succeeded() || !strings.Contains(attempt.ResponseBody(), "nope") {
			t.Errorf("attempt = status %d body %q", attempt.HTTPStatus(), attempt.ResponseBody())
		}
	})

	t.Run("response body is read up to 500 bytes", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 2000)))
		}))
		defer srv.Close()

		attempt, _ := NewDispatcher().Dispatch(context.Background(), endpointFor(srv.URL), deliveryWithAttempts(0))
		if len(attempt.ResponseBody()) != 500 {
			t.Errorf("body len = %d, want 500", len(attempt.ResponseBody()))
		}
	})

	t.Run("connection error is captured in the attempt", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close() // nadie escucha

		attempt, err := NewDispatcher().Dispatch(context.Background(), endpointFor(url), deliveryWithAttempts(1))
		if err != nil {
			t.Fatalf("errors must be captured in the attempt, got %v", err)
		}
		if attempt.HTTPStatus() != 0 || !strings.HasPrefix(attempt.ErrMsg(), "http call:") || attempt.AttemptNum() != 2 {
			t.Errorf("attempt = status %d err %q num %d", attempt.HTTPStatus(), attempt.ErrMsg(), attempt.AttemptNum())
		}
	})

	t.Run("timeout is captured in the attempt", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)

		d := &Dispatcher{client: &http.Client{Timeout: 20 * time.Millisecond}}
		attempt, err := d.Dispatch(context.Background(), endpointFor(srv.URL), deliveryWithAttempts(0))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempt.HTTPStatus() != 0 || !strings.Contains(attempt.ErrMsg(), "http call:") {
			t.Errorf("attempt = status %d err %q", attempt.HTTPStatus(), attempt.ErrMsg())
		}
	})

	t.Run("invalid URL is captured as build request error", func(t *testing.T) {
		attempt, err := NewDispatcher().Dispatch(context.Background(), endpointFor("http://[::1"), deliveryWithAttempts(0))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempt.HTTPStatus() != 0 || !strings.HasPrefix(attempt.ErrMsg(), "build request:") || attempt.AttemptNum() != 1 {
			t.Errorf("attempt = status %d err %q", attempt.HTTPStatus(), attempt.ErrMsg())
		}
	})
}
