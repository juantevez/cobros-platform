package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// WebhookEndpoint es el agregado raíz del contexto Webhook.
//
// Representa la URL del comercio donde la plataforma enviará notificaciones
// cuando ocurran eventos relevantes (pagos, desembolsos, KYC, etc.).
//
// La autenticidad de cada entrega se verifica con una firma HMAC-SHA256
// calculada sobre el payload usando el Secret del endpoint.
//
// Nota sobre el Secret: en Fase 3 se almacena en texto claro. En producción
// usar AES-256-GCM con una clave de aplicación rotable.
type WebhookEndpoint struct {
	id          EndpointID
	tenantID    TenantID
	url         string
	secret      string   // HMAC secret; mostrar solo al crear
	secretHint  string   // últimos 4 chars del secret (para identificación)
	events      []string // event types suscritos, ej: ["payment.captured", "payout.confirmed"]
	active      bool
	description string
	createdAt   time.Time
	updatedAt   time.Time

	domainEvents []Event
}

// NewWebhookEndpoint crea un endpoint con el secret pre-generado.
// El secret se debe entregar al comercio y almacenar de forma segura.
func NewWebhookEndpoint(
	id EndpointID,
	tenantID TenantID,
	rawURL string,
	secret string,
	events []string,
	description string,
) (*WebhookEndpoint, error) {
	if rawURL == "" {
		return nil, ErrEndpointURLEmpty
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return nil, fmt.Errorf("invalid webhook URL: %w", err)
	}
	if len(events) == 0 {
		return nil, ErrNoEventsSubscribed
	}

	hint := secret
	if len(hint) > 4 {
		hint = "..." + hint[len(hint)-4:]
	}

	now := time.Now().UTC()
	e := &WebhookEndpoint{
		id:          id,
		tenantID:    tenantID,
		url:         rawURL,
		secret:      secret,
		secretHint:  hint,
		events:      normalizeEvents(events),
		active:      true,
		description: description,
		createdAt:   now,
		updatedAt:   now,
	}

	e.record(EndpointRegisteredEvent{
		baseEvent:  newBase(tenantID.String()),
		EndpointID: id.String(),
		TenantID:   tenantID.String(),
		URL:        rawURL,
		Events:     e.events,
	})

	return e, nil
}

// ReconstituteWebhookEndpoint reconstruye desde el repositorio.
func ReconstituteWebhookEndpoint(
	id EndpointID, tenantID TenantID,
	rawURL, secret, secretHint, description string,
	events []string, active bool,
	createdAt, updatedAt time.Time,
) *WebhookEndpoint {
	return &WebhookEndpoint{
		id: id, tenantID: tenantID,
		url: rawURL, secret: secret, secretHint: secretHint,
		description: description,
		events:      events, active: active,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// Deactivate desactiva el endpoint. Las deliveries pendientes no se enviarán.
func (e *WebhookEndpoint) Deactivate() {
	e.active = false
	e.updatedAt = time.Now().UTC()
	e.record(EndpointDeactivatedEvent{
		baseEvent:  newBase(e.tenantID.String()),
		EndpointID: e.id.String(),
		TenantID:   e.tenantID.String(),
	})
}

// SubscribesTo retorna true si el endpoint está suscrito al event type dado.
// Soporta comparación sin versión: "payment.captured" coincide con "payment.captured.v1".
func (e *WebhookEndpoint) SubscribesTo(eventType string) bool {
	normalized := stripVersion(eventType)
	for _, ev := range e.events {
		if ev == normalized || ev == eventType {
			return true
		}
	}
	return false
}

// ComputeSignature calcula la firma HMAC-SHA256 del payload.
// Retorna el hex del digest, listo para el header X-Cobros-Signature.
func (e *WebhookEndpoint) ComputeSignature(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(e.secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (e *WebhookEndpoint) ID() EndpointID          { return e.id }
func (e *WebhookEndpoint) TenantID() TenantID      { return e.tenantID }
func (e *WebhookEndpoint) URL() string             { return e.url }
func (e *WebhookEndpoint) Secret() string          { return e.secret }
func (e *WebhookEndpoint) SecretHint() string      { return e.secretHint }
func (e *WebhookEndpoint) Events() []string        { return e.events }
func (e *WebhookEndpoint) Active() bool            { return e.active }
func (e *WebhookEndpoint) Description() string     { return e.description }
func (e *WebhookEndpoint) CreatedAt() time.Time    { return e.createdAt }
func (e *WebhookEndpoint) UpdatedAt() time.Time    { return e.updatedAt }

func (e *WebhookEndpoint) PullEvents() []Event {
	evs := e.domainEvents
	e.domainEvents = nil
	return evs
}

func (e *WebhookEndpoint) record(ev Event) { e.domainEvents = append(e.domainEvents, ev) }

// ── helpers ───────────────────────────────────────────────────────────────────

func normalizeEvents(events []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(events))
	for _, ev := range events {
		n := stripVersion(strings.TrimSpace(ev))
		if n != "" {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				result = append(result, n)
			}
		}
	}
	return result
}

// stripVersion quita el sufijo de versión: "payment.captured.v1" → "payment.captured"
func stripVersion(eventType string) string {
	parts := strings.Split(eventType, ".")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) > 1 && last[0] == 'v' {
			allDigits := true
			for _, c := range last[1:] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return strings.Join(parts[:len(parts)-1], ".")
			}
		}
	}
	return eventType
}
