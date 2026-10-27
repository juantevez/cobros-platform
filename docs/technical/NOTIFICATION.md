# Notification — Documentación técnico-funcional

> **Bounded Context:** `context/notification`
> **Migración:** [`000011_notification_init`](../../migrations/000011_notification_init.up.sql)
> **Streams NATS consumidos:** `PAYMENT` (`payment.>`), `PAYOUT` (`payout.>`), `ONBOARDING` (`onboarding.application.>`), `AUTH` (`auth.tenant.suspended.>`)

---

## 1. Propósito

Notification es el contexto que **avisa al comercio por email** cuando ocurre algo relevante
en la plataforma (pago capturado, payout confirmado, KYC aprobado/rechazado, suspensión de
cuenta). No publica eventos propios: es un **consumidor puro** del bus.

Responsabilidades:

1. Suscribirse a eventos de dominio en NATS JetStream (4 consumers).
2. Decidir si el tenant quiere recibir esa notificación (preferencias; default = habilitado).
3. Resolver el destinatario (email override de la preferencia o email del admin del tenant).
4. Renderizar un template hardcodeado por event type.
5. Enviar el email (SMTP o simulado) y **persistir el intento** como log en `notifications`.
6. Exponer API HTTP de historial y preferencias.

Diseño deliberadamente **no crítico**: cualquier fallo de envío se loguea y se ackea; nunca
bloquea ni reintenta (a diferencia de Webhooks, que sí tiene retry poller).

### Estado actual de las integraciones

| Puerto | Implementación cableada | Comentario |
|---|---|---|
| `EmailSender` (worker) | **`LogSender` siempre** | `cmd/worker/main.go` instancia `LogSender` sin mirar `SMTP_*`; en producción **no salen emails reales** por este camino. |
| `EmailSender` (API) | `SMTPSender` si `cfg.SMTPHost != ""`, else `LogSender` | El use case de envío está cableado en `cmd/api/main.go` pero `_ = sendNotif`: **el API no envía**, solo sirve consultas/preferences. |
| `UserContactReader` | `postgres.ContactReader` | Lee `users ⨝ memberships` (tablas del contexto Auth, misma BD): primer admin `status='active'` ordenado por `memberships.created_at ASC`. |
| `NotificationRepository` / `PreferenceRepository` | `postgres.*` (pgx pool) | Ver §8. |
| Consumers NATS | 4, `MaxDeliver: 3` | Pero el handler siempre retorna `nil` → **nunca hay redelivery efectivo** (§7.3). |

### Fuera de alcance (hoy)

- SMS y push (`ChannelSMS`/`ChannelPush` comentados en `vo.go`, Fase 4).
- Templates en BD editables por operador (hardcodeados en `domain/template.go`, Fase 4).
- Reintentos de envío, dedupe anti-doble-email, digest/agregación.
- Publicar eventos propios (ningún outbox de `notification.*`).
- Notificaciones in-app / panel de campanita.

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Notificación** (`Notification`) | Registro (log) de un intento de envío a un destinatario. Una fila por intento. |
| **Preferencia** (`NotificationPreference`) | Configuración por `(tenant, event_type, channel)`: activa/desactivada + email override. Sin fila = default habilitado. |
| **Template** | Par subject+body HTML con placeholders `{{.key}}`, indexado por event type normalizado (sin versión). |
| **Event type normalizado** | Subject sin sufijo de versión: `payment.captured.v1` → `payment.captured`. |
| **Destinatario (recipient)** | Email de la preferencia si existe; si no, email del admin del tenant. |
| **Skip silencioso** | Resultado "no se envió y no se registró nada": evento sin template, sin destinatario, o preferencia deshabilitada. |

---

## 3. Flujo funcional

```mermaid
sequenceDiagram
    autonumber
    participant N as NATS JetStream
    participant C as EventConsumer
    participant UC as SendNotificationUseCase
    participant T as Templates (dominio)
    participant P as PreferenceRepo
    participant CR as ContactReader
    participant E as EmailSender
    participant R as NotifRepo

    N->>C: msg "payment.captured.v1" {tenant_id, ...}
    C->>C: unmarshal payload; sin tenant_id o malformado → ack nil
    C->>C: buildTemplateData(subject, payload); desconocido → ack nil
    C->>UC: Execute(SendNotificationCmd{TenantID, EventType, TemplateData})
    UC->>T: FindTemplate(eventType)
    alt sin template
        UC-->>C: nil (skip silencioso, sin registro)
    end
    UC->>P: FindByTenantAndEvent(tenant, et)
    Note over UC,P: error ≠ not_found → skip + warn (sin registro)<br/>pref deshabilitada → "" → skip silencioso<br/>pref con recipient_email → se usa ese email
    UC->>CR: GetTenantAdminEmail(tenant) (si no override)
    Note over UC,CR: error → skip + warn (sin registro)
    UC->>T: tmpl.Render(data) → subject, body
    UC->>E: Send(to, subject, body)
    alt éxito
        UC->>UC: notif.MarkSent()
    else fallo
        UC->>UC: notif.MarkFailed(err) + log Error
    end
    UC->>R: Save(notif)  ← siempre (sent o failed)
    UC-->>C: nil → ack
```

---

## 4. Modelo de dominio

### 4.1 Agregado `Notification`

Log append-only de intentos. Invariante de estado:

```
pending ──MarkSent()──▶ sent            (setea sent_at = now UTC)
        ──MarkFailed(reason)──▶ failed  (guarda error_msg; sent_at queda nil)
```

No hay transiciones desde `sent`/`failed` (los métodos no validan el estado previo, pero el
use case los llama exactamente una vez sobre agregados recién creados). Campos: id (UUID),
tenant_id, event_type (crudo, con `.v1`), channel, recipient_email, subject (ya renderizado),
status, error_msg, sent_at, created_at.

### 4.2 Entidad `NotificationPreference`

Compuesta por `(tenant_id, event_type, channel)`; `enabled bool` + `recipient_email` opcional
(vacío = usar admin del tenant). Sin máquina de estados: upsert directo.

### 4.3 Value Objects (`vo.go`)

| VO | Validación | Notas |
|---|---|---|
| `NotificationID` | ninguno | `NewNotificationID()` genera UUID v4. No existe `ParseNotificationID`. |
| `TenantID` | `ParseTenantID` exige UUID válido | Se valida en cada entrypoint de aplicación. |
| `Channel` | solo `"email"` | `sms`/`push` devuelven `ErrInvalidChannel`. |
| `NotificationStatus` | **sin parseo defensivo** | Reconstruido desde BD con cast directo (`NotificationStatus(statusStr)`); la CHECK de la migración es la única garantía. |

### 4.4 Errores (`errors.go`)

| Error | Usado realmente | Dónde |
|---|---|---|
| `ErrPreferenceNotFound` | ✅ | Retornado por el repo pg; el use case lo interpreta como "sin preferencia". |
| `ErrNoRecipientEmail` | ✅ | Retornado por `ContactReader` cuando no hay admin activo. |
| `ErrNotificationNotFound` | ❌ muerta | No hay operación de búsqueda por ID. |
| `ErrTemplateNotFound` | ❌ muerta | `FindTemplate` devuelve `(tmpl, bool)`, nunca el error. |
| `ErrSendFailed` | ❌ muerta | El fallo de SMTP se propaga como error crudo del sender. |
| `ErrInvalidChannel` | ✅ | `ParseChannel`. |

---

## 5. Casos de uso

### 5.1 `SendNotificationUseCase` (interno, invocado por el consumer)

Firma: `Execute(ctx, SendNotificationCmd{TenantID, EventType, TemplateData}) error`.

Reglas:
1. `ParseTenantID` inválido → **único error propagado** (el consumer lo loguea y ackea igual).
2. Sin template → `nil`, sin registro ("el evento no es notificable").
3. Preferencia explícitamente deshabilitada → skip silencioso, **sin fila en `notifications`**.
4. Fallo buscando preferencia o email del admin → skip + `Warn`, sin registro.
5. Siempre que llega a intentar el envío se persiste una fila (`sent` o `failed`).
6. Fallo persistiendo el log → solo `Error` en log; `Execute` devuelve `nil`.

### 5.2 `ListNotificationsUseCase`

`limit <= 0 → 50` (sin tope máximo, ver §10.7). Proyecta a `NotificationView` con fechas RFC3339.

### 5.3 `UpdatePreferenceUseCase`

Rechaza event types sin template registrado (`FindTemplate`). **Siempre fuerza `channel=email`**
y hace `Upsert` con `enabled` y `recipient_email` tal cual vienen del comando (ver bugs §10.5–10.6).

### 5.4 `GetPreferencesUseCase`

Devuelve la unión: cada event type del catálogo con su preferencia guardada o el default
(`enabled=true`, sin override). Nota: itera claves del mapa `Templates`, por lo que puede
listar tanto `kyc.approved` como `payout.confirmed`… y también inconsistencias de formato (§10.4).

---

## 6. API HTTP

Montadas bajo el grupo `protected` (JWT/middleware de tenant) → `/api/v1/notifications`:

| Método | Ruta | Descripción | Errores |
|---|---|---|---|
| `GET` | `/api/v1/notifications?limit=50` | Historial del tenant, `created_at DESC`. Respuesta: `{notifications:[…], count:n}` | 500 genérico |
| `GET` | `/api/v1/notifications/preferences` | Preferencias completas (defaults incluidos) | 500 genérico |
| `PUT` | `/api/v1/notifications/preferences/:eventType` | Body `{"enabled":bool,"recipient_email":string}` → 204 | 400 body inválido; 400 con `err.Error()` filtrado |

Observaciones:
- El `tenant_id` sale de `postgres.TenantIDFromContext` y **el error se descarta** (`tenantID, _ :=`).
- `Save`/`Upsert` usan `ConnFromContext(ctx, pool)`: dentro de una tx con tenant setiado aplica
  RLS; fuera de tx (todos los casos de este contexto) operan con el pool directo (§8).

---

## 7. Eventos

### 7.1 Consumidores suscritos

| Consumer | Stream | Filtro | MaxDeliver |
|---|---|---|---|
| `notification-payment-consumer` | `PAYMENT` | `payment.>` | 3 |
| `notification-payout-consumer` | `PAYOUT` | `payout.>` | 3 |
| `notification-onboarding-consumer` | `ONBOARDING` | `onboarding.application.>` | 3 |
| `notification-auth-consumer` | `AUTH` | `auth.tenant.suspended.>` | 3 |

### 7.2 Matriz evento → template → datos

`eventPayload` extrae: `tenant_id, payment_id, amount, currency, failure_reason, payment_method, bank_reference, rejection_reason`.

| Subject recibido | ¿Notifica? | Placeholders construidos | Observaciones |
|---|---|---|---|
| `payment.captured.v1` | ✅ | payment_id, amount, currency, payment_method | — |
| `payment.failed.v1` | ✅ | payment_id, amount, currency, failure_reason | — |
| `payment.refunded.v1` | 🔴 | payment_id, **amount=`"0.00"`**, currency | El evento real publica `refund_amount`, no `amount` → el email dice *"Reembolso procesado: 0.00"* (bug §10.2) |
| `payout.confirmed.v1` | ✅ | amount, currency, bank_reference | — |
| `payout.failed.v1` | ✅ | amount, currency, failure_reason | — |
| `onboarding.application.approved.v1` | ⚠️ | `{}` | El template resuelto es `kyc.approved` (normalización); funciona porque el body no usa placeholders (§10.4) |
| `onboarding.application.rejected.v1` | ✅ | rejection_reason | Template `kyc.rejected` |
| `auth.tenant.suspended.v1` | ✅ | `{}` | — |
| `payment.risk_rejected.v1` | ❌ | código muerto: Payment **no publica** ese subject (riesgo rechaza vía `payment.failed.v1`) | — |
| `payout.initiated.v1`, `payout.unknown.v1`, `onboarding.application.submitted.v1`, `onboarding.application.more_info.v1` | ❌ | `buildTemplateData` → `nil` → ack sin acción | — |

Formato de monto: `fmt.Sprintf("%.2f", float64(amount)/100)` — división fija por 100, sin
considerar monedas sin decimales subunidad (COP/CLP mostrarían `1000.00`).

### 7.3 Semántica de ack

El handler **retorna `nil` en todos los caminos** (payload malformado, sin tenant, sin template,
incluso si `Execute` devuelve error → solo log). Por tanto `MaxDeliver: 3` es decorativo:
ningún mensaje es nackeado ni reentregado. Idempotencia de consumo: **inexistente** — dos
deliveries del mismo evento generarían dos emails y dos filas (posible ante restarts con
mensajes aún sin ack, o si JetStream reentrega por ack-outstanding).

---

## 8. Persistencia

Dos tablas (migración `000011`), ambas con `ENABLE ROW LEVEL SECURITY`:

- **`notifications`**: PK `id UUID`; CHECK `status IN ('pending','sent','failed')`; índice
  `(tenant_id, created_at DESC)`. `error_msg`/`sent_at` nullable (`nullStr` mapea `""→NULL`).
- **`notification_preferences`**: PK compuesta `(tenant_id, event_type, channel)`;
  `recipient_email NULL = usar admin`.

**RLS es inaplicable en la práctica** para este contexto: las políticas dependen de
`current_setting('app.current_tenant')`, que solo se setea como `SET LOCAL` dentro de
transacciones (`pkg/postgres/txmanager.go`). Ningún método de estos repos usa la tx manager,
así que leen/escriben por el pool sin variable de sesión. Además `ListByTenant` (ambos repos)
usa `r.pool` directamente incluso si existiera contexto de tx. La aislación real recae en el
filtro `WHERE tenant_id=$1` del código de aplicación.

Escrituras: `INSERT` simple en `notifications` (nunca UPDATE posterior; el estado final ya se
determina en memoria antes de persistir) y `UPSERT … ON CONFLICT` en preferencias.

---

## 9. Seguridad y cumplimiento

- Endpoints detrás de JWT + middleware de tenant; no hay API de escritura de `notifications` (solo lectura del propio tenant).
- **PII**: `recipient_email` y `subject` quedan persistidos en claro en `notifications` sin política de retención ni anonimización.
- `recipient_email` del `PUT` **no se valida como email** (§10.5): cabe cualquier string, que además se inyecta crudo en headers SMTP (§10.11).
- Errores internos del `PUT` se devuelven al cliente vía `err.Error()` (§10.8).
- Credenciales SMTP vía env vars (`SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/USE_TLS`); `tlsConfig` inyectable solo en tests.

---

## 10. Limitaciones conocidas y deuda técnica

Ordenadas por severidad. 🔴 bug funcional · 🟡 comportamiento cuestionable · ⚪ deuda/fase 4.

1. 🔴 **Los emails nunca se envían en producción (worker).** `cmd/worker/main.go` fija
   `notifEmailSender := NewLogSender(...)` ignorando la config SMTP; solo registra
   "📧 [DEV] Email simulado". El `SMTPSender` cableado en el API es inalcanzable porque
   `_ = sendNotif` (§1).
2. 🔴 **`payment.refunded.v1` renderiza `0.00`**: `buildTemplateData` lee `p.Amount` (JSON
   `amount`), pero `PaymentRefundedEvent` publica `refund_amount`. Corregir mapeando
   `refund_amount` (el test actual fija el comportamiento incorrecto, §11).
3. 🔴 **Sin dedupe/idempotencia de consumo**: reentrega del mismo evento ⇒ doble email y
   doble fila. No existe clave natural UNIQUE (p. ej. `(tenant_id, event_subject, nats_msg_id)`).
4. 🔴 **Claves de preferencia inconsistentes entre endpoints**: `UpdatePreferenceUseCase`
   guarda la preferencia bajo el event type **tal cual lo manda el cliente** (acepta
   `payment.captured.v1` porque `FindTemplate` lo resuelve), pero `resolveRecipient` busca
   por el subject crudo del evento y `GetPreferencesUseCase` lista claves del catálogo
   **normalizadas** (`payment.captured`). Apagar `payment.captured.v1` vía PUT puede no tener
   efecto y la GET muestra otra clave que la PUT aceptó. Unificar a event type normalizado
   de extremo a extremo.
5. 🟡 **`PUT preferences` no valida `recipient_email`** (formato, longitud) ni canonicaliza;
   `enabled=false` + email sigue aceptado (inofensivo pero ruidoso).
6. 🟡 **La PUT écrasa sin merge**: siempre upsertea con el `recipient_email` del body; omitir
   el campo deja el override vacío aunque hubiera uno previo.
7. 🟡 **`limit` sin tope máximo** en `GET /notifications` (`limit=1000000` pasa; el error de
   `strconv.Atoi` se descarta → cae a 50 solo si falta o no es numérico).
8. 🟡 **Manejo plano de errores HTTP**: fallos de BD → 500 genérico sin distinguir causa;
   errores de dominio del `PUT` → 400 con `err.Error()` expuesto al cliente.
9. 🟡 **RLS declarada pero inaplicada** (§8): aislamiento depende exclusivamente del
   `WHERE tenant_id=$1` en código.
10. 🟡 **`MaxDeliver: 3` engañoso** (§7.3): handler siempre ackea; no hay reintento ni DLQ propia.
11. 🟡 **Header injection menor en SMTP**: `buildMessage` interpola `to`/`subject` crudos en
    `To:`/`Subject:`; el subject proviene de un template controlado, pero el
    `recipient_email` viene de input del tenant (§10.5).
12. ⚪ **Templates hardcodeados** en el binario: cambiar copy implica redeploy (Fase 4: tabla
    + edición por operador).
13. ⚪ **Cobertura de eventos incompleta**: `payout.initiated`, `payout.unknown`,
    `onboarding.application.submitted/more_info`, `dispute.*`, `webhook.*`,
    `reconciliation.*` no notifican. Los templates `kyc.approved`/`kyc.rejected` responden a
    subjects que ningún producer publica hoy (llegan solo vía onboarding, §7.2).
14. ⚪ **Código muerto**: `payment.risk_rejected.v1` en `buildTemplateData` (nunca publicado);
    centinelas `ErrNotificationNotFound`, `ErrTemplateNotFound`, `ErrSendFailed` (§4.4);
    `ParseChannel` descartado (`ch, _ :=`) al reconstruir desde BD.
15. ⚪ **`time.Now()` interno en el dominio** (`NewNotification`, `MarkSent`,
    `NewNotificationPreference`): reloj no inyectable → tests de fecha dependientes de tiempo
    relativo; conviene pasar clock desde aplicación.
16. ⚪ **Moneda/formato**: división fija /100, HTML plano embebido, sin i18n ni plantillas por tenant.

---

## 11. Testing

Paquete completo verde con Go 1.25.0 (`go test ./context/notification/...`), 6 paquetes:

| Paquete | Enfoque |
|---|---|
| `domain` | Máquina de estados de `Notification`, VOs, `ParseChannel`, `stripVersion`/`FindTemplate`, render de placeholders, preferencias. |
| `application` | `mocks_test.go` (repos/sender/contact fake) + casos de `SendNotification` (skip silencioso, pref deshabilitada, fallo de envío persiste `failed`, fallo de save no propaga), preferences y list. |
| `http` | Handler con gin test mode: rutas, binding, códigos de respuesta. |
| `nats` | `buildTemplateData` por subject —incluye el caso muerto `risk_rejected` y el refund con `amount=0` **como comportamiento actual fijado por test**— y ack silencioso. |
| `email` | `fake_smtp_test.go`: servidor SMTP TLS local self-signed (cert inyectado vía `tlsConfig`), STARTTLS y `buildMessage`. |
| `postgres` | `pgxmock/v4`: SQL exacto de Save/List/Upsert/Find incl. `COALESCE`, `nullStr`, contacto admin. |

Brechas de cobertura relevantes: no se testea el wiring de `cmd/worker` (de allí que el bug
§10.1 pase inadvertido); el test de `payment.refunded.v1` fija el valor hoy incorrecto
(documentado, no corregido); no hay prueba de doble delivery/dedupe (la funcionalidad no existe).

---

## 12. Mapa de código

```
context/notification/
├── domain/
│   ├── notification.go     # agregado log: pending→sent/failed
│   ├── preference.go       # entidad preferencia (default: habilitado)
│   ├── template.go         # catálogo Templates{} + FindTemplate/stripVersion + Render
│   ├── vo.go               # IDs, Channel (solo email), Status
│   └── errors.go           # 6 centinelas (3 muertos, ver §4.4)
├── application/
│   ├── ports.go            # NotificationRepository, PreferenceRepository, EmailSender, UserContactReader
│   ├── commands.go         # SendNotificationCmd, UpdatePreferenceCmd, Views
│   ├── send_notification.go# SendNotificationUseCase + ListNotificationsUseCase
│   └── preferences.go      # Update/GetPreferencesUseCase
└── infrastructure/adapters/
    ├── inbound/http/handler.go          # 3 endpoints + RegisterRoutes
    ├── inbound/nats/event_consumer.go   # 4 consumers + eventPayload + buildTemplateData
    └── outbound/
        ├── email/smtp_sender.go         # SMTPSender (TLS/STARTTLS), LogSender, buildMessage
        └── postgres/repos.go            # pgNotificationRepository, pgPreferenceRepository, ContactReader
```

Wiring: `cmd/api/main.go` (solo API de lectura/preferencias + sender SMTP condicional) y
`cmd/worker/main.go` (consumidores NATS + LogSender fijo → ver bug §10.1).
