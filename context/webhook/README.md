# context/webhook — Webhooks

Entrega eventos de dominio al endpoint HTTP del comercio, con firma HMAC-SHA256, reintentos automáticos con backoff escalonado e historial de intentos.

---

## Responsabilidades

- Registrar y gestionar los endpoints webhook de cada comercio
- Crear una `WebhookDelivery` por endpoint suscrito cada vez que ocurre un evento relevante
- Despachar las deliveries vía HTTP con firma HMAC-SHA256 verificable
- Reintentar automáticamente en caso de fallo, con backoff escalonado
- Exponer historial completo de entregas e intentos para debugging

---

## Estructura

```
context/webhook/
├── domain/
│   ├── errors.go      # Errores de negocio
│   ├── vo.go          # IDs, DeliveryStatus, RetrySchedule, DeliveryAttempt
│   ├── events.go      # EndpointRegistered, EndpointDeactivated, DeliveryExhausted
│   ├── endpoint.go    # Agregado WebhookEndpoint + ComputeSignature (HMAC-SHA256)
│   └── delivery.go    # Entidad WebhookDelivery: FSM + RecordAttempt + reintentos
├── application/
│   ├── ports.go            # EndpointRepository, DeliveryRepository, HTTPDispatcher,
│   │                       # SecretGenerator, EventPublisher, Clock
│   ├── commands.go         # DTOs + EndpointView, DeliveryView, AttemptView
│   ├── register_endpoint.go    # Registrar URL + generar secret HMAC
│   ├── dispatch_event.go       # Crear deliveries al recibir un evento NATS
│   ├── retry_delivery.go       # Despacho HTTP + RetryPoller (goroutine de fondo)
│   └── list_deliveries.go      # Consultas de endpoints y deliveries
└── infrastructure/adapters/
    ├── inbound/
    │   ├── http/webhook_handler.go    # Endpoints REST de gestión
    │   └── nats/event_consumer.go     # Consumer de payment, payout, onboarding, auth
    └── outbound/
        ├── postgres/                  # endpoint_repo + delivery_repo + attempt_repo
        ├── http/dispatcher.go         # POST al comercio con headers HMAC
        └── crypto/secret_generator.go # "whsec_" + 32 bytes hex
```

---

## Dominio

### `WebhookEndpoint`

Representa la URL del comercio para recibir notificaciones.

- `SubscribesTo("payment.captured.v1")` → true si está suscrito a `"payment.captured"`
  (la comparación ignora el sufijo de versión)
- `ComputeSignature(payload)` → `HMAC-SHA256(secret, payload)` en hex

### `WebhookDelivery`

FSM de entrega de un evento a un endpoint:

```
pending → delivered   (endpoint respondió 2xx)
pending → failed      (fallo; hay reintento programado)
failed  → pending     (RetryPoller activa cuando llega next_retry_at)
failed  → exhausted   (agotados todos los reintentos)
```

**Schedule de reintentos** (configurable vía `RetrySchedule`):

| Intento | Delay desde el fallo |
|---|---|
| 1 (inmediato) | — |
| 2 | + 30 segundos |
| 3 | + 2 minutos |
| 4 | + 10 minutos |
| 5 | + 1 hora |
| — | exhausted |

`RecordAttempt(attempt, now)` actualiza el estado y calcula `next_retry_at`.

---

## Flujo completo

```
1. Evento en NATS (ej: payment.captured.v1)
        ↓
2. EventConsumer.handle()
   → DispatchEventUseCase: busca endpoints activos suscritos
   → Por cada endpoint: crea WebhookDelivery(status=pending)
   → Guarda en BD, ack NATS inmediatamente (sin esperar HTTP)
        ↓
3. RetryPoller (cada 5s)
   → SELECT deliveries WHERE status IN ('pending','failed') AND next_retry_at <= now
   → Por cada una: HTTPDispatcher.Dispatch()
        ↓
4. HTTPDispatcher
   → POST <url_comercio> con:
       Content-Type:       application/json
       X-Cobros-Signature: t=<unix>,v1=<hmac de "<unix>.<body>">
       X-Cobros-Event:     payment.captured
       X-Cobros-Delivery:  <delivery_id>
       X-Cobros-Timestamp: <unix>
        ↓
5. Si 2xx → delivery.Succeed() → status=delivered
   Si error → delivery.RecordAttempt(fail) → status=failed, calcula next_retry_at
   Si agotado → delivery.Exhaust() → status=exhausted → emite DeliveryExhaustedEvent
```

---

## Firma HMAC — verificación en el comercio

`X-Cobros-Signature: t=<unix>,v1=<hex>`, donde `v1 = HMAC-SHA256(secret, "<t>." + body crudo)`. El timestamp va firmado: el comercio debe rechazar los requests fuera de su tolerancia (se recomiendan 5 minutos) para que uno capturado no se pueda reenviar. Hay que verificar sobre el **body crudo**, antes de parsear el JSON.

```python
# Python
import hmac, hashlib, time

TOLERANCE = 300  # segundos

def verify(secret: str, header: str, raw_body: bytes, now: float) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))  # {"t": "...", "v1": "..."}
    t, received = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(now - int(t)) > TOLERANCE:
        return False  # viejo (posible replay) o mal formado
    expected = hmac.new(secret.encode(), t.encode() + b"." + raw_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, received)

# if not verify(secret, request.headers["X-Cobros-Signature"], request.body, time.time()): return 401
```

```javascript
// Node.js
const crypto = require('crypto')
const TOLERANCE = 300 // segundos

function verify(secret, header, rawBody, now) {
  const parts = Object.fromEntries(header.split(',').map(p => p.split(/=(.*)/s).slice(0, 2)))
  const t = parts.t, received = parts.v1 || ''
  if (!/^\d+$/.test(t || '') || Math.abs(now - Number(t)) > TOLERANCE) return false
  const expected = crypto.createHmac('sha256', secret).update(`${t}.`).update(rawBody).digest('hex')
  return received.length === expected.length &&
    crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(received))
}

// if (!verify(secret, req.get('X-Cobros-Signature'), rawBody, Date.now() / 1000)) return res.status(401).end()
```

---

## API

```
POST   /api/v1/webhooks/endpoints                  Registrar endpoint     [JWT]
GET    /api/v1/webhooks/endpoints                  Listar endpoints       [JWT]
DELETE /api/v1/webhooks/endpoints/:endpointID      Desactivar endpoint    [JWT]
GET    /api/v1/webhooks/deliveries?limit=50        Historial de entregas  [JWT]
GET    /api/v1/webhooks/deliveries/:deliveryID     Detalle de entrega     [JWT]
POST   /api/v1/webhooks/deliveries/:deliveryID/retry  Reintento manual   [JWT]
```

### Registrar endpoint

```json
POST /api/v1/webhooks/endpoints
{
  "url": "https://mi-comercio.com/webhooks/cobros",
  "events": ["payment.captured", "payment.failed", "payout.confirmed"],
  "description": "Producción"
}
```

Respuesta (el secret solo se muestra una vez):
```json
{
  "endpoint_id": "...",
  "secret":      "whsec_a3f1b2c4d5e6...",
  "secret_hint": "...c4d5",
  "note": "Store the secret securely. It will not be shown again."
}
```

### Payload enviado al comercio

```json
{
  "event_type":  "payment.captured.v1",
  "event_id":    "evt_uuid",
  "delivery_id": "dlv_uuid",
  "occurred_at": "2026-06-01T12:00:00Z",
  "data": {
    "payment_id": "...",
    "amount": 10000,
    "currency": "ARS"
  }
}
```

---

## Eventos suscritos desde otros contextos

| Stream NATS | Filter | Eventos despachados |
|---|---|---|
| PAYMENT | `payment.>` | `payment.captured`, `payment.failed`, `payment.refunded` |
| PAYOUT | `payout.>` | `payout.initiated`, `payout.confirmed`, `payout.failed`, `payout.unknown` |
| ONBOARDING | `onboarding.>` | `onboarding.application.{submitted,sent_for_review,approved,rejected,more_info,reopened}` |
| AUTH | `auth.tenant.>` | `auth.tenant.created`, `auth.tenant.activated`, `auth.tenant.suspended` |
| DISPUTE | `dispute.>` | `dispute.opened` (con el `deadline` para responder), `dispute.resolved` |

Detalle completo (modelo, API, persistencia, limitaciones) en [docs/technical/WEBHOOK.md](../../docs/technical/WEBHOOK.md).

---

## Dependencias del contexto

```
Produce eventos → stream WEBHOOK (webhook.endpoint.registered.v1, etc.)
Consume eventos → PAYMENT, PAYOUT, ONBOARDING, AUTH, DISPUTE

Depende de → pkg/postgres
             pkg/outbox (EventPublisher)
             pkg/eventbus (Consumer)
```
