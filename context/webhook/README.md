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
       X-Cobros-Signature: sha256=<hmac>
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

```python
# Python
import hmac, hashlib

signature_header = request.headers["X-Cobros-Signature"]  # "sha256=abc123..."
received_sig     = signature_header[7:]                   # quita "sha256="
expected_sig     = hmac.new(secret.encode(), request.body, hashlib.sha256).hexdigest()

if not hmac.compare_digest(expected_sig, received_sig):
    return 401  # firma inválida
```

```javascript
// Node.js
const sig = crypto.createHmac('sha256', secret).update(rawBody).digest('hex')
if (!crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(receivedSig))) {
  return res.status(401).end()
}
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
  "event_type":  "payment.captured",
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
| PAYOUT | `payout.>` | `payout.confirmed`, `payout.failed` |
| ONBOARDING | `onboarding.>` | `kyc.approved`, `kyc.rejected` |
| AUTH | `auth.tenant.>` | `auth.tenant.activated`, `auth.tenant.suspended` |

---

## Dependencias del contexto

```
Produce eventos → stream WEBHOOK (webhook.endpoint.registered.v1, etc.)
Consume eventos → PAYMENT, PAYOUT, ONBOARDING, AUTH

Depende de → pkg/postgres
             pkg/outbox (EventPublisher)
             pkg/eventbus (Consumer)
```
