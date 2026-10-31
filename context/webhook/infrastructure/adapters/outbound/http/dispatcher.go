package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
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
//	X-Cobros-Signature:    t=<unix>,v1=<hex(HMAC-SHA256(secret, "<unix>." + body))>
//	X-Cobros-Event:        payment.captured
//	X-Cobros-Delivery:     <delivery_id>
//	X-Cobros-Timestamp:    <unix> (el mismo t de la firma)
//
// El comercio debe verificar la firma y la frescura:
//
//	t, v1    = parse(request.Header("X-Cobros-Signature"))
//	expected = HMAC-SHA256(secret, t + "." + raw_body)
//	assert hmac.Equal(expected, v1) && |now - t| <= 5 min
type Dispatcher struct {
	client *http.Client
}

// NewDispatcher crea un Dispatcher con timeout de 30s que no sigue redirects y
// que solo conecta a las IPs que acepta guard (ver DestinationGuard). No usa
// el proxy del entorno: con proxy, el dialer validaría la IP del proxy y no la
// del destino.
//
// Ante 301/302/303 el http.Client repite el request como GET sin body: el
// destino podía responder 2xx y la delivery quedaba entregada sin que el
// comercio recibiera el evento. Un 3xx es un intento fallido; el comercio
// tiene que registrar la URL final.
func NewDispatcher(guard *DestinationGuard) *Dispatcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guard.control,
	}).DialContext

	return &Dispatcher{
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (d *Dispatcher) Dispatch(
	ctx context.Context,
	endpoint *domain.WebhookEndpoint,
	delivery *domain.WebhookDelivery,
) (domain.DeliveryAttempt, error) {
	payload := delivery.Payload()
	timestamp := time.Now().Unix()
	signature := endpoint.SignatureHeader(timestamp, payload)
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
	req.Header.Set("X-Cobros-Signature", signature)
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

	var errMsg string
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		errMsg = fmt.Sprintf("redirect not followed (Location: %q): register the final URL", resp.Header.Get("Location"))
	}

	attempt := domain.NewDeliveryAttempt(
		delivery.AttemptCount()+1,
		resp.StatusCode,
		string(bodyBytes),
		errMsg,
		elapsed,
	)
	return attempt, nil
}
