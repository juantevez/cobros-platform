# Webhook (Webhooks salientes) — Documentación técnico-funcional

> **Bounded Context:** `context/webhook`
> **Migraciones:** [`000009_webhook_init`](../../migrations/000009_webhook_init.up.sql), reclamo de deliveries en [`000040_webhook_delivery_claim`](../../migrations/000040_webhook_delivery_claim.up.sql), RLS en [`000041_webhook_rls`](../../migrations/000041_webhook_rls.up.sql)
> **Stream NATS:** `WEBHOOK` (`webhook.>`)

---

## 1. Propósito

Webhook entrega los eventos de dominio de la plataforma al **endpoint HTTP del comercio**. El comercio registra una URL y los tipos de evento que le interesan. Cada vez que ocurre uno de esos eventos en su tenant, la plataforma le hace un `POST` firmado con HMAC-SHA256 y reintenta con backoff si el endpoint falla.

| Principio | Cómo se implementa |
|---|---|
| Consumer desacoplado del comercio | El consumer NATS solo **crea** la delivery (`pending`). El POST lo hace el `RetryPoller`, así que un endpoint lento no frena el ack a NATS. |
| Idempotencia por evento | `UNIQUE (endpoint_id, event_id)`. Además hay un chequeo previo en `DispatchEvent`, y una re-entrega de NATS no duplica la delivery. |
| Autenticidad | `X-Cobros-Signature: t=<unix>,v1=<HMAC-SHA256(secret, "<unix>." + body)>` (timestamp firmado, contra replay) con un secret `whsec_…` que se muestra **una sola vez** al registrar. |
| Entrega *at-least-once* | Si falla la persistencia después del POST, la delivery se vuelve a despachar. El comercio debe deduplicar por `delivery_id` (o por `event_id`). |
| Historial | Cada intento HTTP queda en `webhook_delivery_attempts`: status, primeros 500 bytes de la respuesta (cortados en un carácter completo, con UTF-8 inválido reemplazado y sin `NUL`, para que siempre entren en `TEXT`), error y duración. |

Fuera de alcance: webhooks **entrantes** (del PSP), edición o reactivación de endpoints, rotación del secret, reenvío de deliveries agotadas (§10).

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Endpoint** | URL del comercio más la lista de tipos de evento suscriptos. Agregado raíz `WebhookEndpoint`. |
| **Secret** | Clave HMAC del endpoint (`whsec_` + 64 hex). Se devuelve solo en la respuesta del alta. |
| **Secret hint** | `...` + últimos 4 caracteres del secret, para identificarlo sin exponerlo. |
| **Delivery** | La entrega de **un** evento a **un** endpoint. Entidad `WebhookDelivery` con su máquina de estados. |
| **Attempt** | Un POST concreto de una delivery, con su resultado. |
| **Envelope** | Cuerpo JSON que recibe el comercio: metadatos de la entrega más el evento original en `data`. |
| **Tipo de evento** | Subject NATS sin versión: `payment.captured.v1` → `payment.captured`. Las suscripciones se guardan así y matchean cualquier versión. |

---

## 3. Flujo funcional

```mermaid
sequenceDiagram
    autonumber
    participant BC as Payment / Payout / Onboarding / Auth
    participant N as NATS JetStream
    participant C as EventConsumer (worker)
    participant DB as Postgres
    participant P as RetryPoller (worker, cada 5s)
    participant M as Endpoint del comercio

    BC->>N: evento (outbox relay)
    N->>C: payment.captured.v1
    C->>DB: endpoints activos del tenant suscriptos
    C->>DB: INSERT delivery (pending, next_retry_at = now)
    C-->>N: ack
    P->>DB: deliveries pending/failed con next_retry_at <= now (máx. 50, endpoint activo)
    P->>DB: Claim (claimed_until = now + 2 min, si otro la tiene se saltea)
    P->>M: POST envelope + X-Cobros-Signature (hasta 10 en paralelo, timeout 30s)
    M-->>P: 2xx / error
    P->>DB: tx: UPDATE delivery (libera el reclamo) + INSERT attempt
    opt quinto fallo
        P->>DB: misma tx: outbox webhook.delivery.exhausted.v1
    end
```

1. Un BC publica un evento por outbox, y el relay lo envía a su stream.
2. El `EventConsumer` toma `tenant_id` del payload y busca los endpoints **activos** del tenant suscriptos a ese tipo. Por cada uno crea una delivery `pending`, disponible de inmediato. Si un endpoint falla, sigue con los demás y al final hace *nak*. En la re-entrega solo se crean las que faltan.
3. El `RetryPoller` despacha las deliveries vencidas. Si el endpoint responde 2xx, queda `delivered`. Si no, queda `failed` con el próximo reintento programado (§4.3). El quinto fallo la deja `exhausted` y emite `webhook.delivery.exhausted.v1`.
4. El comercio puede ver el historial y forzar un reintento manual (§6).

---

## 4. Modelo de dominio

### 4.1 Agregado `WebhookEndpoint`

| Campo | Regla |
|---|---|
| `url` | Obligatoria; URL absoluta `http`/`https` con host (`validateURL`). El destino debe ser público (§6.2). |
| `events` | Al menos uno. Se normalizan con trim, sin versión y sin duplicados (`normalizeEvents`). No se validan contra un catálogo. |
| `secret` / `secretHint` | Generado por `SecretGenerator` (`crypto/rand`, 32 bytes). Se guarda **en claro**. |
| `active` | `true` al crear; `Deactivate()` lo pasa a `false`. No hay reactivación. |

- `SubscribesTo(eventType)` compara sin versión.
- `ComputeSignature(payload)` devuelve el HMAC-SHA256 hex.
- Eventos: `EndpointRegisteredEvent` y `EndpointDeactivatedEvent`.

### 4.2 Entidad `WebhookDelivery`

```
pending ──2xx──────────────► delivered   (final)
pending ──fallo────────────► failed      (next_retry_at programado)
failed  ──2xx──────────────► delivered
failed  ──fallo────────────► failed
failed  ──5º fallo─────────► exhausted   (final, emite DeliveryExhaustedEvent)
```

- `RecordAttempt(attempt, now)` incrementa `attemptCount`, agrega el intento y decide el estado. Es éxito si el status es 2xx, y falla en un estado final.
- `MarkReadyForRetry` (`failed → pending`) existe en el dominio, pero ningún caso de uso la llama: el poller despacha `failed` directamente.

### 4.3 Reintentos

| Intento | Cuándo |
|---|---|
| 1 | Inmediato (`next_retry_at = created_at`) |
| 2 | 30 s después del fallo 1 |
| 3 | 2 min después del fallo 2 |
| 4 | 10 min después del fallo 3 |
| 5 | 1 h después del fallo 4 |
| — | Fallo 5 → `exhausted` |

`MaxAttempts = len(RetrySchedule) + 1`; un test lo fija. Los reintentos manuales cuentan contra el mismo presupuesto.

### 4.4 Errores

| Error | HTTP |
|---|---|
| `ErrEndpointNotFound`, `ErrDeliveryNotFound` (incluye recurso de otro tenant) | 404 |
| `ErrInvalidID` (endpoint, delivery o tenant que no es UUID), `ErrEndpointURLEmpty`, `ErrInvalidEndpointURL`, `ErrNoEventsSubscribed`, `ErrDestinationNotAllowed` | 400 |
| `ErrDeliveryNotRetryable` (delivery `delivered`/`exhausted`), `ErrEndpointInactive` | 422 |
| `ErrDeliveryInProgress` (otro proceso la tiene reclamada, §9) | 409 |
| Cualquier otro | 500 |

`ErrDuplicateDelivery` está declarado pero no se usa.

---

## 5. Casos de uso

| Caso de uso | Disparador | Qué hace |
|---|---|---|
| `RegisterEndpoint` | `POST /webhooks/endpoints` | Genera el secret, crea el endpoint, valida el destino con `DestinationGuard` (§6.2) y, en la misma tx, publica `webhook.endpoint.registered.v1`. Devuelve el secret. |
| `DeactivateEndpoint` | `DELETE /webhooks/endpoints/:id` | Verifica el tenant, `Deactivate()`, `UPDATE` y publica `webhook.endpoint.deactivated.v1` en tx. |
| `ListEndpoints` | `GET /webhooks/endpoints` | Endpoints del tenant (sin secret). |
| `DispatchEvent` | Consumer NATS | Crea una delivery por endpoint suscripto (§3). |
| `RetryDelivery.Execute` | `POST /webhooks/deliveries/:id/retry` | Reintento manual **síncrono**: despacha dentro del request (hasta 30 s). Acepta `pending` y `failed`. |
| `RetryDelivery.dispatch` | `RetryPoller` y el anterior | Carga el endpoint (si está inactivo devuelve `ErrEndpointInactive` sin registrar intento), **reclama** la delivery (§9; si otro proceso la tiene, `ErrDeliveryInProgress` sin POST), hace el POST, `RecordAttempt` y, en **una sola tx**, `UPDATE` de la delivery, `INSERT` del intento y `webhook.delivery.exhausted.v1` si corresponde. Si algo falla, la tx se descarta y la delivery queda como estaba: el poller la vuelve a despachar (*at-least-once*). |
| `ListDeliveries` / `GetDelivery` | `GET /webhooks/deliveries[/:id]` | Historial del tenant (lista sin intentos, detalle con intentos). |

---

## 6. API HTTP

Todas las rutas están en el grupo `protected` (`/api/v1`, `JWTMiddleware`) detrás de un único guard, `RequirePermission(webhooks:manage, webhooks:write)`: con JWT solo pasa `admin`; con API key, el scope `webhooks:write`. Las lecturas también lo exigen, porque el historial expone los payloads completos. Sin permiso → 403.

| Método | Ruta | Respuesta |
|---|---|---|
| `POST` | `/webhooks/endpoints` | 201 `{endpoint_id, secret, secret_hint, note}` |
| `GET` | `/webhooks/endpoints` | 200 `{endpoints, count}` |
| `DELETE` | `/webhooks/endpoints/:endpointID` | 204 |
| `GET` | `/webhooks/deliveries?limit=50` | 200 `{deliveries, count}`: más recientes primero, sin intentos |
| `GET` | `/webhooks/deliveries/:deliveryID` | 200 delivery con `attempts` |
| `POST` | `/webhooks/deliveries/:deliveryID/retry` | 204 (el resultado del POST queda en la delivery); 409 si se está despachando |

```json
POST /api/v1/webhooks/endpoints
{ "url": "https://comercio.example.com/hooks", "events": ["payment.captured", "payout.confirmed"], "description": "Producción" }
```

### 6.1 Lo que recibe el comercio

```
POST <url>
Content-Type:       application/json
User-Agent:         cobros-platform-webhooks/1.0
X-Cobros-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, "<unix>." + body)>
X-Cobros-Event:     payment.captured            (sin versión)
X-Cobros-Delivery:  <delivery_id>
X-Cobros-Timestamp: <unix, momento del intento> (el mismo t de la firma)
```

```json
{
  "event_type":  "payment.captured.v1",
  "event_id":    "<id del evento de dominio>",
  "delivery_id": "<delivery_id>",
  "occurred_at": "2026-10-30T12:00:00Z",
  "data":        { "payment_id": "…", "tenant_id": "…", "amount": 10000, "currency": "ARS", "…": "…" }
}
```

- `event_type` lleva la versión, a diferencia del header.
- `event_id` es el `Nats-Msg-Id`, o sea el ID del evento de dominio. Ningún evento de los streams consumidos serializa `id` ni `event_id`.
- `occurred_at` sale del header `occurred-at` que agrega el relay, y si falta se usa `now`.
- `data` es el payload interno del evento **tal cual**: el contrato público queda acoplado al interno.
- El body se guarda como `JSONB`, así que el orden de las claves y los espacios pueden diferir del original. La firma se calcula sobre los bytes que efectivamente se envían.

Verificación en el comercio (ejemplos en Python y Node en el [README del contexto](../../context/webhook/README.md)):

1. Separar `t` y `v1` del header.
2. Rechazar si `|now - t|` supera la tolerancia (se recomiendan 5 min): un request capturado no se puede reenviar después, porque cambiar `t` invalida la firma.
3. Comparar en tiempo constante `v1` contra `hex(HMAC-SHA256(secret, t + "." + raw_body))`, sobre el body **crudo**.

Cada intento lleva su propio `t`, así que la firma cambia entre reintentos. `v1` identifica el esquema: un cambio futuro puede agregar `v2` sin romper a quien verifica `v1`.

Solo un **2xx** cuenta como entregado. Los redirects **no se siguen**: un 3xx es un intento fallido y su `error` indica el `Location`, para que el comercio registre la URL final. Seguirlos convertía un 301/302/303 en un GET sin body que podía dar la delivery por entregada.


### 6.2 Destinos permitidos (SSRF)

El worker hace el POST desde la red interna, y el tenant ve los primeros 500 bytes de la respuesta. Por eso los webhooks solo salen hacia **direcciones públicas**. Se bloquean loopback, privadas (RFC 1918 y ULA), link-local (incluida la metadata del cloud, `169.254.169.254`), CGNAT (`100.64.0.0/10`), `0.0.0.0/8`, multicast y no especificadas, también como IPv4 mapeada en IPv6. `DestinationGuard` ([destination_guard.go](../../context/webhook/infrastructure/adapters/outbound/http/destination_guard.go)) aplica dos controles:

| Momento | Control | Resultado |
|---|---|---|
| Alta (`RegisterEndpoint`) | `localhost` / `*.localhost`, IP literal interna, o host que **hoy** resuelve a alguna IP interna. Si el host no resuelve, se acepta. | 400 `ErrDestinationNotAllowed` |
| Cada POST | `net.Dialer.Control` valida la IP **ya resuelta** de cada conexión. Cubre DNS rebinding y hosts que pasan a resolver a una IP interna después del alta. | Intento fallido, con `error` = `webhook destination not allowed…` |

El transporte ignora `HTTP_PROXY`/`HTTPS_PROXY`: con proxy, el dialer validaría la IP del proxy y no la del destino. `WEBHOOK_ALLOW_PRIVATE_TARGETS=true` (api y worker) desactiva ambos controles. Es **solo para desarrollo**, para probar contra un servidor local.
---

## 7. Eventos

### 7.1 Publicados (stream `WEBHOOK`)

| Evento | Cuándo | Payload | Consumidores |
|---|---|---|---|
| `webhook.endpoint.registered.v1` | Alta | `endpoint_id`, `tenant_id`, `url`, `events` | Audit |
| `webhook.endpoint.deactivated.v1` | Baja | `endpoint_id`, `tenant_id` | Audit |
| `webhook.delivery.exhausted.v1` | Quinto fallo | `delivery_id`, `endpoint_id`, `tenant_id`, `event_type` | Nadie |

Los tres se publican por outbox en la tx del caso de uso. El de agotada va en la misma tx que el estado `exhausted` y el último intento.

### 7.2 Consumidos

| Durable | Stream | Filtro | `MaxDeliver` |
|---|---|---|---|
| `webhook-payment-consumer` | `PAYMENT` | `payment.>` | 3 |
| `webhook-payout-consumer` | `PAYOUT` | `payout.>` | 3 |
| `webhook-onboarding-consumer` | `ONBOARDING` | `onboarding.>` | 3 |
| `webhook-auth-consumer` | `AUTH` | `auth.tenant.>` | 3 |

- Los tipos que llegan son, por ejemplo, `payment.{captured,failed,refunded}`, `payout.{initiated,confirmed,failed,unknown}`, `onboarding.application.{submitted,sent_for_review,approved,rejected,more_info,reopened}` y `auth.tenant.{created,activated,suspended}`.
- No se consumen `dispute.*`, `ledger.*`, `billing.*`, `compliance.*` ni `reconciliation.*`. Compliance queda afuera a propósito (anti *tipping-off*, ver AUTH.md §7.2).
- El consumer hace *ack* sin reintentar si el payload no es JSON, si no trae `tenant_id` o si el `tenant_id` es inválido. Un error de base de datos hace *nak*.

---

## 8. Persistencia

| Tabla | Clave / restricciones | Notas |
|---|---|---|
| `webhook_endpoints` | PK `id`; índices `(tenant_id, active)` y GIN sobre `events` | `secret` en texto claro. |
| `webhook_deliveries` | PK `id`; FK `endpoint_id`; `UNIQUE (endpoint_id, event_id)`; `CHECK status` | `payload JSONB` (el envelope). `claimed_until`: reclamo vigente de un despachador (§9). Índice parcial `next_retry_at WHERE status IN ('pending','failed')` para el poller. |
| `webhook_delivery_attempts` | PK `id`; FK compuesta `(delivery_id, tenant_id)` → `webhook_deliveries (id, tenant_id)`; `UNIQUE (delivery_id, attempt_num)` | `tenant_id` del intento = el de su delivery. `response_body` son los primeros 500 bytes. |

- **RLS** (migración 000041): las tres tablas tienen dos políticas.
  - **Aislamiento por tenant:** `NULLIF(current_setting('app.current_tenant', true), '')::uuid`.
  - **Operador de plataforma:** `app.platform_operator = 'on'`, **solo lectura** (`FOR SELECT`).
- **Quién fija la sesión:** cada método de repositorio corre en la sesión del tenant (`withTenant`, [rls.go](../../context/webhook/infrastructure/adapters/outbound/postgres/rls.go)). Usa el tenant de la entidad, o el del contexto en las búsquedas por ID.
  - El consumer pone en el contexto el `tenant_id` del evento.
  - El poller lista como operador (cross-tenant) y despacha cada delivery con su propio tenant: endpoint, reclamo y tx del resultado.
  - El API usa el tenant del JWT.
- El aislamiento no depende del rol de DB: `rls_test.go` corre con un rol sin `BYPASSRLS` (`webhook_rls_probe`).
- `FindActiveByTenantAndEvent` busca `events @> ARRAY[tipo]` con y sin versión.
- `ListDueForRetry` hace `JOIN` con los endpoints activos, así que las deliveries de un endpoint desactivado no ocupan el batch, pero quedan `pending`/`failed` para siempre. Tampoco lista las que tienen un reclamo vigente.

---

## 9. Concurrencia

- **Consumer:** hay un durable por stream. Si dos re-entregas del mismo evento compiten, el `UNIQUE (endpoint_id, event_id)` hace fallar a la segunda, que vuelve a entrar por *nak* y encuentra la delivery ya creada.
- **Poller:** cada ciclo despacha hasta 50 deliveries con 10 POST simultáneos y espera a que terminen antes del siguiente tick, así que dentro de un proceso no hay ciclos superpuestos.
- **Reclamo** (`claimed_until`): el poller, el reintento manual y cualquier réplica del worker pueden tener la misma delivery. Antes del POST, `Claim` hace un `UPDATE` condicional: solo pasa si sigue `pending`/`failed`, con el mismo `attempt_count` que se leyó, y sin reclamo vigente. Pone `claimed_until = now + 2 min`, así que de N despachadores gana uno solo. El que pierde no envía: el poller lo saltea en silencio y el reintento manual responde 409. Si el proceso muere a mitad del envío, el reclamo vence y el poller la retoma.
- **Bloqueo optimista:** el `UPDATE` final exige el `attempt_count` previo y libera el reclamo. Si otro proceso ya registró un intento (reclamo vencido), devuelve `ErrDeliveryInProgress` y no lo pisa.
- **Persistencia del intento:** `UPDATE` de la delivery, `INSERT` del intento y evento de agotada van en una transacción.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Severidad | Tema | Detalle |
|---|---|---|---|
| 1 | Baja | `limit` sin tope | `GET /deliveries?limit=1000000` carga todo el historial del tenant. |
| 2 | Baja | Desactivar dos veces | `DeactivateEndpoint` sobre un endpoint ya inactivo vuelve a emitir `webhook.endpoint.deactivated.v1` y pisa `updated_at`. |
| 3 | Baja | Suscripciones sin catálogo | `events` acepta cualquier string: un typo o un tipo que no existe (el README anterior documentaba `kyc.approved`) da un endpoint que nunca recibe nada, sin error. |
| 4 | Producto | Alcance | Secret en claro y sin rotación. Endpoints sin edición ni reactivación. No se puede reenviar una delivery `exhausted`, y las de un endpoint desactivado quedan colgadas. El reintento manual es síncrono y consume el presupuesto de reintentos. Un endpoint caído (timeout de 30 s) ocupa el batch y demora a todos los tenants: no hay *circuit breaker* ni cola por endpoint. `data` expone el evento interno tal cual. Se permite `http`. Historial sin paginación, filtros ni retención. `webhook.delivery.exhausted.v1` no lo consume nadie (no se avisa al comercio). |

---

## 11. Testing

| Capa | Archivos | Tipo |
|---|---|---|
| Dominio | `domain/*_test.go` | Validación de URL y eventos, firma de `<t>.<body>` (vector calculado aparte en Python, y distinta con otro `t`), FSM de la delivery, schedule y `MaxAttempts`, truncado y saneado del body (sin partir caracteres, UTF-8 inválido, `NUL`). |
| Aplicación | `application/*_test.go` | Alta (incluido el destino rechazado sin guardar nada) y baja con publicación en tx, `DispatchEvent` (idempotencia, fallo parcial), `RetryDelivery` (tenant ajeno, final, endpoint inactivo, exhausted, estado + evento en la misma tx y error de publish que la descarta, reclamo con su vencimiento y `attempt_count`, delivery reclamada por otro sin POST), poller con concurrencia (saltea en silencio las reclamadas y despacha cada delivery con su tenant), listados. |
| HTTP | `inbound/http/webhook_handler_test.go` | Handlers, mapeo de errores (IDs mal formados en la URL → 400) y guard en cada ruta (403; `RegisterRoutes` exige el guard). |
| NATS | `inbound/nats/event_consumer_test.go` | Extracción de tenant (y su paso al contexto, para RLS), `event_id` y `occurred_at`; casos que hacen *ack* sin reintentar. |
| Dispatcher | `outbound/http/dispatcher_test.go`, `destination_guard_test.go` | Headers, firma `t=…,v1=…` verificada como lo haría el comercio (`t` = `X-Cobros-Timestamp`), errores de red, redirects no seguidos (301/302/303/307/308) y destino interno bloqueado en el dial (sin tocar el servidor) contra `httptest`. Rangos internos, chequeo del alta con resolver falso (incluido un host con una IP pública y otra interna), control del dialer y modo desarrollo. |
| Postgres | `outbound/postgres/repo_test.go`, `integration_test.go`, `rls_test.go` | `pgxmock` para el SQL de cada método (dentro de una tx simulada) y para la sesión RLS: tenant de la entidad, tenant del llamador, operador en el listado del poller. Integración contra PostgreSQL (`DATABASE_URL`, se saltan si no hay DB): intento con body multibyte cortado y `NUL`; 8 reclamos simultáneos de la misma delivery (gana uno), reclamo vigente fuera del poller, reclamo vencido retomado, `UPDATE` que libera el reclamo y rechaza una escritura con `attempt_count` viejo. RLS con un rol sin `BYPASSRLS`: aislamiento de endpoints, deliveries e intentos entre tenants, sin tenant no se ve nada, el operador lista cross-tenant pero no escribe, y el flujo consumer → poller entrega bajo RLS. |

```bash
go test ./context/webhook/...
```

---

## 12. Mapa de código

```
context/webhook/
├── domain/
│   ├── endpoint.go     # WebhookEndpoint, validateURL, normalizeEvents, ComputeSignature
│   ├── delivery.go     # WebhookDelivery: RecordAttempt, FSM
│   ├── vo.go           # IDs, DeliveryStatus, RetrySchedule, MaxAttempts, DeliveryAttempt
│   ├── events.go       # webhook.endpoint.{registered,deactivated}, webhook.delivery.exhausted
│   └── errors.go
├── application/
│   ├── ports.go        # repos, HTTPDispatcher, SecretGenerator, EventPublisher, Clock, IncomingEvent
│   ├── register_endpoint.go   # RegisterEndpoint + DeactivateEndpoint
│   ├── dispatch_event.go      # WebhookEnvelope + DispatchEvent
│   ├── retry_delivery.go      # RetryDelivery + RetryPoller
│   └── list_deliveries.go     # ListDeliveries, GetDelivery, ListEndpoints
└── infrastructure/adapters/
    ├── inbound/http/webhook_handler.go     # rutas /webhooks/*
    ├── inbound/nats/event_consumer.go      # 4 durables
    └── outbound/
        ├── http/dispatcher.go              # POST firmado, timeout 30s, sin redirects ni proxy
        ├── http/destination_guard.go       # SSRF: alta + control del dialer
        ├── crypto/secret_generator.go      # whsec_<64 hex>
        └── postgres/{endpoint,delivery}_repo.go, rls.go   # cada método en la sesión RLS
```

Wiring: `cmd/api/main.go` (rutas; el `DispatchEvent` se construye pero no se usa) y `cmd/worker/main.go` (4 consumers + `RetryPoller` cada 5 s).
