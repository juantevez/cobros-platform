package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// Dispatcher implementa application.HTTPDispatcher.
// Hace un POST al endpoint del comercio con la firma HMAC-SHA256 en headers.
//
// Headers enviados al comercio:
//
//	Content-Type:          application/json
//	X-Cobros-Signature:    sha256=<hex(HMAC-SHA256(secret, body))>
//	X-Cobros-Event:        payment.captured
//	X-Cobros-Delivery:     <delivery_id>
//	X-Cobros-Timestamp:    <unix_timestamp>
//
// El comercio debe verificar la firma:
//
//	expected = HMAC-SHA256(secret, raw_body)
//	received = request.Header("X-Cobros-Signature")[7:]  // quita "sha256="
//	assert hmac.Equal(expected, received)
type Dispatcher struct {
	client *http.Client
}

// NewDispatcher crea un Dispatcher con timeout de 30s.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (d *Dispatcher) Dispatch(
	ctx context.Context,
	endpoint *domain.WebhookEndpoint,
	delivery *domain.WebhookDelivery,
) (domain.DeliveryAttempt, error) {
	payload := delivery.Payload()
	signature := endpoint.ComputeSignature(payload)
	timestamp := time.Now().Unix()
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL(), bytes.NewReader(payload))
	if err != nil {
		elapsed := time.Since(start).Milliseconds()
		attempt := domain.NewDeliveryAttempt(
			delivery.AttemptCount()+1, 0, "",
			fmt.Sprintf("build request: %v", err), elapsed,
		)
		return attempt, nil // error capturado en el attempt, no propagado
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cobros-Signature", "sha256="+signature)
	req.Header.Set("X-Cobros-Event", delivery.EventType())
	req.Header.Set("X-Cobros-Delivery", delivery.ID().String())
	req.Header.Set("X-Cobros-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("User-Agent", "cobros-platform-webhooks/1.0")

	resp, err := d.client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		attempt := domain.NewDeliveryAttempt(
			delivery.AttemptCount()+1, 0, "",
			fmt.Sprintf("http call: %v", err), elapsed,
		)
		return attempt, nil
	}
	defer resp.Body.Close()

	// Leer hasta 500 bytes de la respuesta para debugging.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 500))

	attempt := domain.NewDeliveryAttempt(
		delivery.AttemptCount()+1,
		resp.StatusCode,
		string(bodyBytes),
		"",
		elapsed,
	)
	return attempt, nil
}
