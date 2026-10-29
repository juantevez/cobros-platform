# cobros-platform

> Backend de una **Plataforma de Cobros** estilo Payment Facilitator (PayFac), construido en Go con DDD y arquitectura hexagonal.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-336791?logo=postgresql)](https://www.postgresql.org/)
[![NATS](https://img.shields.io/badge/NATS-JetStream-27AAE1?logo=natsdotio)](https://nats.io/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

---

## ¿Qué es esto?

`cobros-platform` es un **Payment Facilitator multi-comercio** que permite a negocios (tenants) aceptar pagos de sus clientes y recibir el dinero correspondiente descontando comisiones. Actúa como intermediario entre el comercio, el cliente final y los proveedores de pago externos (adquirentes, billeteras, redes de tarjetas).

**Características principales:**

- 🏢 **Multi-tenant** con aislamiento estricto por Row Level Security en PostgreSQL
- 📒 **Libro mayor de doble partida** como única fuente de verdad del dinero
- ⚡ **Eventos de dominio** publicados de forma confiable via Transactional Outbox + NATS JetStream
- 🔐 **Autenticación** con JWT (HS256) + API keys para integraciones server-to-server
- 🏗️ **Modular monolith** extraíble a microservicios por contexto acotado

---

## Stack tecnológico

| Componente | Tecnología |
|---|---|
| Lenguaje | Go 1.25 |
| Base de datos | PostgreSQL 16 (pgx v5, Row Level Security) |
| Mensajería | NATS 2.10 con JetStream |
| Router HTTP | Gin |
| JWT | golang-jwt/jwt v5 (HS256) |
| Hash de contraseñas | Argon2id |
| Migraciones | golang-migrate |
| Contenedores | Docker + Docker Compose |

---

## Arquitectura

### Principios

El sistema aplica **Domain-Driven Design (DDD) táctico** con **arquitectura hexagonal (Ports & Adapters)** dentro de cada contexto acotado. La regla de dependencias es estricta:

```
domain ← application ← adapters (inbound / outbound)
```

El `domain` no conoce a nadie. La `application` define puertos (interfaces) e implementa casos de uso. Los `adapters` conectan el mundo exterior (HTTP, PostgreSQL, NATS) al núcleo.

### Comunicación entre contextos

Los contextos se integran por **eventos de dominio** publicados en NATS JetStream vía el patrón **Transactional Outbox**:

```
Caso de uso
  └── RunInTx {
        repo.Save(agregado)          → PostgreSQL
        outbox.Save(evento)          → outbox_messages (misma tx)
      }

cmd/worker (relay)
  └── cada 1s: lee outbox_messages pendientes
              publica en JetStream (Nats-Msg-Id para deduplicación)
              marca como publicado
```

**Única excepción síncrona:** Payment calcula la comisión de cada cobro llamando in-process a `billing.CalculateFeeUseCase` (adaptador `fees.BillingFeeCalculator` en Payment; Billing no conoce a Payment). Ver [BILLING.md §9](docs/technical/BILLING.md).

### Multi-tenancy

Estrategia: `tenant_id` en cada tabla + **Row Level Security** de PostgreSQL.

El `TxManager` ejecuta `set_config('app.current_tenant', $1, true)` al inicio de cada transacción. Las políticas RLS filtran automáticamente por ese valor. El tenant viaja en `context.Context` desde el middleware HTTP hasta el repositorio.

---

## Estructura del repositorio

```
cobros-platform/
├── cmd/
│   ├── api/            # Servidor HTTP (todos los contextos)
│   └── worker/         # Relay del Outbox → NATS + consumers de eventos + pollers
│
├── context/            # Contextos acotados (Bounded Contexts)
│   ├── auth/           # Auth & Multi-Tenant
│   ├── ledger/         # Libro Mayor (doble partida)
│   ├── audit/          # Bitácora inmutable (hash chain)
│   ├── onboarding/     # KYC/KYB de comercios
│   ├── payment/        # Payment Processing (PSP router, risk, fees)
│   ├── payout/         # Desembolsos al comercio
│   ├── billing/        # Planes de tarifas y cálculo de comisiones
│   ├── reconciliation/ # Conciliación contra el PSP
│   ├── webhook/        # Webhooks salientes con reintentos
│   ├── notification/   # Emails transaccionales
│   ├── dispute/        # Disputas y contracargos
│   ├── reporting/      # Read-model CQRS para dashboard
│   └── compliance/     # Screening y monitoreo AML
│
├── pkg/                # Código transversal (no de dominio)
│   ├── postgres/       # Pool, TxManager, RLS (WithAppTenant/RunWithAppTenant), ConnFromContext
│   ├── eventbus/       # Abstracción sobre NATS JetStream
│   ├── outbox/         # Transactional Outbox (Store + Relay)
│   └── config/         # Carga de configuración desde env vars
│
├── docs/technical/     # Documentación técnico-funcional por contexto
├── migrations/         # SQL versionado (golang-migrate)
├── deploy/docker/      # Docker Compose + Dockerfile multi-stage
└── Makefile
```

### Anatomía de un contexto

```
context/<nombre>/
├── domain/             # Núcleo: agregados, VOs, eventos, errores
│                         Sin dependencias externas. Solo stdlib.
├── application/        # Casos de uso + puertos (interfaces)
│                         Depende solo de domain.
└── infrastructure/
    └── adapters/
        ├── inbound/http/   # Handlers Gin, middleware, router
        └── outbound/
            ├── postgres/   # Implementaciones de repositorios
            └── events/     # EventPublisher → outbox.Store
```

---

## Contextos implementados

La documentación técnico-funcional de cada contexto (modelo, casos de uso, API, eventos, persistencia) vive en [`docs/technical/`](docs/technical/). Abajo se resumen algunos.

| Contexto | Responsabilidad | Doc |
|---|---|---|
| Auth & Multi-Tenant | Tenants, usuarios, roles, JWT y API keys | [AUTH.md](docs/technical/AUTH.md) |
| Ledger | Libro mayor de doble partida, fuente de verdad del dinero | [LEDGER.md](docs/technical/LEDGER.md) |
| Audit | Bitácora inmutable con verificación de cadena | *(pendiente)* |
| Onboarding | Solicitudes KYC/KYB y revisión por operador | [ONBOARDING.md](docs/technical/ONBOARDING.md) |
| Payment Processing | Cobros contra PSP con evaluación de riesgo y comisión | [PAYMENT.md](docs/technical/PAYMENT.md) |
| Payouts | Desembolsos del saldo del comercio a su banco | [PAYOUT.md](docs/technical/PAYOUT.md) |
| Billing & Fees | Planes de tarifas, asignación por tenant y cálculo de comisión | [BILLING.md](docs/technical/BILLING.md) |
| Reconciliation | Conciliación de registros internos contra fuentes externas, con gestión de discrepancias | *(pendiente)* |
| Webhooks | Entrega de eventos a endpoints del comercio con reintentos | *(pendiente)* |
| Notifications | Emails transaccionales con preferencias por tenant | [NOTIFICATION.md](docs/technical/NOTIFICATION.md) |
| Disputes | Disputas y contracargos con impacto en Ledger | [DISPUTE.md](docs/technical/DISPUTE.md) |
| Dashboard & Reporting | Read-model CQRS de volumen, comisiones y saldos | *(pendiente)* |
| Compliance & AML | Screening contra watchlist y monitoreo transaccional | [COMPLIANCE.md](docs/technical/COMPLIANCE.md) |

### ✅ Auth & Multi-Tenant

Gestiona identidad, autenticación y aislamiento por comercio.

| Elemento | Descripción |
|---|---|
| Agregados | `Tenant`, `User`, `ApiKey` |
| Entidades | `Membership` (user-tenant-role) |
| Value Objects | `TenantID`, `UserID`, `Email`, `Role`, `Environment`, `Scope` |
| Casos de uso | `RegisterTenant`, `ActivateTenant`, `SuspendTenant`, `RegisterUser`, `Authenticate`, `RefreshToken`, `Logout`, `IssueApiKey`, `RevokeApiKey`, `AssignRole` |
| Eventos | `TenantCreated`, `TenantActivated`, `TenantSuspended`, `UserRegistered`, `ApiKeyIssued`, `ApiKeyRevoked`, `RoleAssigned` |

**Endpoints:**

```
POST   /api/v1/tenants                          Registrar comercio
POST   /api/v1/auth/login                       Autenticar usuario
POST   /api/v1/auth/refresh                     Renovar tokens
POST   /api/v1/auth/logout                      Cerrar sesión
POST   /api/v1/tenants/:id/users                Registrar usuario  [JWT + admin]
PUT    /api/v1/tenants/:id/members/:uid/role    Asignar rol        [JWT + admin]
POST   /api/v1/tenants/:id/api-keys             Emitir API key     [JWT + admin]
DELETE /api/v1/tenants/:id/api-keys/:kid        Revocar API key    [JWT + admin]
POST   /api/v1/tenants/:id/activate             Activar tenant     [JWT + platform_support]
POST   /api/v1/tenants/:id/suspend              Suspender tenant   [JWT + platform_support]
```

---

### ✅ Ledger / Libro Mayor

Contabilidad de doble partida como única fuente de verdad del dinero.

| Elemento | Descripción |
|---|---|
| Agregados | `Account`, `JournalEntry` (con `Posting` embebido) |
| Value Objects | `Money` (monto en centavos + moneda ISO 4217), `Direction` (debit/credit), `AccountType` |
| Casos de uso | `CreateAccount`, `PostEntry`, `ReverseEntry`, `GetBalance` |
| Eventos | `AccountCreated`, `EntryPosted`, `EntryReversed` |

**Invariantes del dominio:**

- `sum(débitos) == sum(créditos)` — validado en el agregado y reforzado con trigger PostgreSQL `DEFERRABLE INITIALLY DEFERRED`
- `amount > 0` siempre — el sentido lo da `Direction`
- Montos en `BIGINT` (centavos) — nunca punto flotante
- Append-only — las correcciones se hacen con asientos de reversa

**Endpoints:**

```
POST  /api/v1/ledger/accounts                         Crear cuenta contable  [JWT + platform_support]
GET   /api/v1/ledger/accounts/:accountID/balance      Consultar saldo        [JWT]
POST  /api/v1/ledger/entries                          Registrar asiento      [JWT + platform_support]
POST  /api/v1/ledger/entries/:entryID/reverse         Crear reversa          [JWT + platform_support]
```

**Ejemplo de asiento (pago acreditado):**

```json
POST /api/v1/ledger/entries
{
  "idempotency_key": "payment_a1b2c3d4",
  "description": "Pago acreditado - orden #1234",
  "lines": [
    { "account_id": "<in_transit_account>",    "direction": "credit", "amount": 10000, "currency": "ARS" },
    { "account_id": "<merchant_balance_acct>", "direction": "debit",  "amount":  9700, "currency": "ARS" },
    { "account_id": "<platform_fees_account>", "direction": "debit",  "amount":   300, "currency": "ARS" }
  ]
}
```

---

### ✅ Dashboard & Reporting

**Read-model CQRS**: proyecta eventos de otros contextos en tablas desnormalizadas optimizadas para consulta. No tiene agregados ni escribe dinero — solo lee.

| Elemento | Descripción |
|---|---|
| Hechos (read-model) | `PaymentFact` (desde `payment.captured.v1`), `LedgerMovement` (desde `ledger.entry.posted.v1`) |
| Vistas | `VolumePoint`, `RevenueSummary`, `TenantBalance` |
| Casos de uso | `ProjectPaymentCaptured`, `ProjectEntryPosted` (proyección), `GetVolume`, `GetRevenue`, `GetBalances` (consulta) |

**Diseño idempotente:** en vez de contadores incrementales (que se duplicarían ante re-entregas de JetStream), se guardan **hechos inmutables** con inserción `ON CONFLICT DO NOTHING` (clave `payment_id`, y `(entry_id, account_id, direction)`) y se **agrega en tiempo de consulta**. Los consumers de proyección corren en `cmd/worker`.

**Endpoints:**

```
GET  /api/v1/reports/volume?from=&to=&granularity=day   Serie de volumen transaccional  [JWT]
GET  /api/v1/reports/revenue?from=&to=                   Comisiones cobradas por período [JWT]
GET  /api/v1/reports/balances                            Saldo neto por tipo de cuenta   [JWT]
```

> `from`/`to` en formato RFC3339 (opcionales); `granularity` ∈ `day|week|month` (default `day`).

---

### ✅ Compliance & AML

Screening contra listas de vigilancia y monitoreo transaccional. Consume eventos de otros contextos y genera **alertas** que revisa el equipo de cumplimiento de la plataforma (`compliance_officer`, con four-eyes en alertas de riesgo alto o de screening). Un match exacto en lista de sanciones pone al comercio en **hold preventivo reversible** (sin cobros ni payouts) hasta que cumplimiento lo descarta o lo confirma (suspensión).

| Elemento | Descripción |
|---|---|
| Agregados | `Alert` (open → cleared \| confirmed, o open → pending_second_approval → cleared \| confirmed) |
| Value Objects | `AlertType` (sanctions_match, transaction_threshold, transaction_velocity, transaction_volume), `RiskLevel`, `AlertStatus` |
| Entidades | `WatchlistEntry` (lista global sanciones/PEP) |
| Casos de uso | `ScreenApplication`, `MonitorTransaction`, `ResolveAlert`, `ApproveAlertProposal`, `RejectAlertProposal`, `ListAlerts`, `GetAlert`, `AddWatchlistEntry`, `RemoveWatchlistEntry`, `ListWatchlist` |
| Eventos | `AlertRaised`, `AlertReviewProposed`, `AlertProposalRejected`, `AlertResolved`, `SanctionMatched`, `SanctionCleared`, `SanctionConfirmed` |

**Reglas del MVP:**

- **Screening** — al recibir `onboarding.application.submitted.v1`, compara el `legal_name` contra `aml_watchlist` (tokens completos, sin acentos ni puntuación). Match → alerta `sanctions_match`.
- **Umbral** — `payment.captured.v1` con `amount ≥ 10.000,00` → alerta `transaction_threshold`.
- **Velocity** — ≥ 10 pagos capturados del tenant en 10 min → alerta `transaction_velocity` (ventana deslizante con cooldown: una alerta por pico).
- **Volumen** — ≥ 50.000,00 acumulados por moneda en la ventana → alerta `transaction_volume` (ídem, por moneda).

Las reglas de ventana cuentan sobre una proyección propia (`aml_monitored_payments`), no sobre la tabla `payments`.

**Idempotencia:** unicidad `(tenant_id, alert_type, subject)` — una re-entrega de evento no duplica alertas.

**Endpoints:**

```
GET    /api/v1/compliance/alerts?tenant_id=&status=&limit=  Bandeja (todos los comercios) [JWT + compliance_officer|platform_support]
GET    /api/v1/compliance/alerts/:alertID              Ver alerta              [JWT + compliance_officer|platform_support]
POST   /api/v1/compliance/alerts/:alertID/resolve      Disponer o proponer     [JWT + compliance_officer]
POST   /api/v1/compliance/alerts/:alertID/approve      Segunda aprobación      [JWT + compliance_officer ≠ proponente]
POST   /api/v1/compliance/alerts/:alertID/reject       Rechazar propuesta      [JWT + compliance_officer ≠ proponente]
GET    /api/v1/compliance/watchlist                    Listar watchlist        [JWT + compliance_officer|platform_support]
POST   /api/v1/compliance/watchlist                    Agregar entrada         [JWT + compliance_officer|platform_support]
DELETE /api/v1/compliance/watchlist/:entryID           Dar de baja entrada     [JWT + compliance_officer|platform_support]
POST   /api/v1/compliance/watchlist/import?source=&dry_run=  Sincronizar fuente (CSV) [JWT + compliance_officer|platform_support]
```

---

## Roadmap

| Fase | Contexto | Estado |
|---|---|---|
| **Fase 1** | Auth & Multi-Tenant | ✅ Completo |
| **Fase 1** | Ledger / Libro Mayor | ✅ Completo |
| **Fase 1** | Audit & Observability | ✅ Completo |
| **Fase 2** | Onboarding (KYC/KYB) | ✅ Completo |
| **Fase 2** | Payment Processing | ✅ Completo |
| **Fase 2** | Fraud & Risk | ✅ Completo |
| **Fase 3** | Payouts / Desembolsos | ✅ Completo |
| **Fase 3** | Billing & Fees | ✅ Completo |
| **Fase 3** | Reconciliation | ✅ Completo |
| **Fase 4** | Webhooks & Eventos | ✅ Completo |
| **Fase 4** | Notifications | ✅ Completo |
| **Fase 4** | Dashboard & Reporting | ✅ Completo |
| **Fase 5** | Disputes & Chargebacks | ✅ Completo |
| **Fase 5** | Compliance & AML | ✅ Completo |

---

## Levantar el entorno local

### Pre-requisitos

- Go 1.25+
- Docker y Docker Compose
- [golang-migrate](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

### Quick start

```bash
# 1. Clonar el repositorio
git clone https://github.com/juantevez/cobros-platform.git
cd cobros-platform

# 2. Levantar PostgreSQL y NATS
make up

# 3. Descargar dependencias
go mod tidy

# 4. Ejecutar migraciones
make migrate

# 5. Terminal 1: servidor HTTP (puerto 8080)
make run-api

# 6. Terminal 2: worker del Outbox relay
make run-worker
```

### Variables de entorno

| Variable | Default (dev) | Descripción |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Dirección del servidor HTTP |
| `HTTP_READ_TIMEOUT_SEC` | `10` | Timeout de lectura HTTP |
| `HTTP_WRITE_TIMEOUT_SEC` | `30` | Timeout de escritura HTTP |
| `DATABASE_URL` | `postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable` | DSN de PostgreSQL |
| `NATS_URL` | `nats://localhost:4222` | URL de NATS |
| `JWT_SECRET` | *(valor de dev)* | Secreto para firmar JWT (mín. 32 chars) |
| `OUTBOX_INTERVAL` | `1s` | Frecuencia del relay del outbox |
| `OUTBOX_BATCH_SIZE` | `50` | Mensajes por ciclo del relay |
| `DB_MAX_CONNS` | `25` | Máximo de conexiones al pool |
| `DB_MIN_CONNS` | `5` | Mínimo de conexiones al pool |
| `SMTP_HOST` | *(vacío)* | Servidor SMTP del worker. Vacío ⇒ los emails se loguean en stdout (dev) |
| `SMTP_PORT` | `587` | Puerto SMTP |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | *(vacío)* | Credenciales SMTP |
| `SMTP_FROM` | `noreply@cobros-platform.com` | Remitente de los emails |
| `SMTP_USE_TLS` | `false` | `true` ⇒ TLS implícito (puerto 465); si no, STARTTLS cuando el servidor lo ofrece |

> ⚠️ **En producción**, `JWT_SECRET` debe generarse con `openssl rand -hex 32` y gestionarse con un secret manager.

### Comandos útiles

```bash
make up            # Levantar PostgreSQL + NATS (Docker)
make down          # Bajar la infraestructura
make migrate       # Aplicar migraciones pendientes
make migrate-down  # Revertir la última migración
make test          # Correr tests con -race
make lint          # Linter (requiere golangci-lint)
make build         # Compilar ambos binarios en bin/
make run-api       # go run ./cmd/api
make run-worker    # go run ./cmd/worker (relay + consumers)
```

---

## Decisiones arquitectónicas clave

| ID | Decisión | Motivo |
|---|---|---|
| ADR-1 | Modular monolith: un binario, múltiples contextos | Simplicidad inicial; contextos extraíbles sin reescribir el dominio |
| ADR-2 | Multi-tenant: `tenant_id` + Row Level Security | Aislamiento fuerte, escalable a miles de comercios |
| ADR-3 | Inter-contexto solo por eventos (JetStream) | Bajo acoplamiento; permite extraer microservicios gradualmente |
| ADR-4 | Transactional Outbox para publicar eventos | Atomicidad entre cambio de datos y publicación |
| ADR-5 | Ledger append-only + doble partida | Integridad financiera y auditabilidad completa del dinero |
| ADR-6 | Montos en `BIGINT` (centavos) | Elimina errores de redondeo de punto flotante |
| ADR-7 | Hash de contraseñas con Argon2id | Resistente a GPU y side-channel attacks (recomendación OWASP) |
| ADR-8 | Versionado de eventos en el subject NATS | Evolución de esquemas sin romper consumidores |

---

## Estructura de eventos NATS

Convención de subjects: `<contexto>.<agregado>.<hecho>.<versión>`

Subjects publicados hoy, por stream:

| Stream | Subjects |
|---|---|
| `AUTH` (`auth.>`) | `auth.tenant.{created,activated,suspended}.v1`, `auth.user.{registered,suspended}.v1`, `auth.apikey.{issued,revoked}.v1`, `auth.role.assigned.v1` |
| `LEDGER` (`ledger.>`) | `ledger.account.created.v1`, `ledger.entry.{posted,reversed}.v1` |
| `ONBOARDING` (`onboarding.>`) | `onboarding.application.{submitted,sent_for_review,approved,rejected,more_info,reopened}.v1` |
| `PAYMENT` (`payment.>`) | `payment.{captured,failed,refunded}.v1` |
| `PAYOUT` (`payout.>`) | `payout.{initiated,confirmed,failed,unknown}.v1` |
| `BILLING` (`billing.>`) | `billing.plan.{created,assigned,updated,deactivated}.v1` |
| `WEBHOOK` (`webhook.>`) | `webhook.endpoint.{registered,deactivated}.v1`, `webhook.delivery.exhausted.v1` |
| `RECONCILIATION` (`reconciliation.>`) | `reconciliation.run.completed.v1` |
| `DISPUTE` (`dispute.>`) | `dispute.{opened,resolved}.v1` |
| `COMPLIANCE` (`compliance.>`) | `compliance.alert.{raised,review_proposed,proposal_rejected,resolved}.v1`, `compliance.sanction.{matched,cleared,confirmed}.v1` (internos, los consume Auth) |
| `NOTIFICATION` (`notification.>`) | *(stream creado; sin eventos publicados)* |

Streams definidos en [`pkg/eventbus/stream.go`](pkg/eventbus/stream.go). Productores y consumidores de cada subject: ver el doc del contexto.

---

## Flujo del dinero (end-to-end)

```
Cliente paga
    ↓
[Fraud & Risk] evalúa la operación (antes de capturar)
    ↓
[Billing & Fees] calcula la comisión del plan del tenant (in-process)
    ↓
[Payment Processing] captura contra PSP → payment.captured.v1
    ↓
[Ledger] registra asiento:
    in_transit         CREDIT  $100
    merchant_balance   DEBIT   $ 97
    platform_fees      DEBIT   $  3
    ↓
[Outbox → NATS] publica ledger.entry.posted.v1 (lo consumen Reporting y Audit)
    ↓
[Payouts] calcula saldo disponible del comercio
    ↓
[Payouts] transfiere al banco del comercio
    ↓
[Ledger] registra asiento del desembolso:
    merchant_balance   CREDIT  $ 97
    payout_transit     DEBIT   $ 97
```

---

## Contribución y convenciones

### Agregar un nuevo contexto

1. Crear `context/<nombre>/domain/` — sin imports de infraestructura
2. Crear `context/<nombre>/application/ports.go` — interfaces de repositorios y servicios
3. Crear los casos de uso en `context/<nombre>/application/`
4. Implementar los adapters en `context/<nombre>/infrastructure/adapters/`
5. Agregar la migración en `migrations/`
6. Cablear en `cmd/api/main.go` y `eventbus/stream.go`

### Convenciones de código

- **Errores de dominio**: definidos en `domain/errors.go`, sin detalles de infraestructura
- **Reconstitución**: cada agregado tiene una función `ReconstituteFoo(...)` para reconstruir desde la base sin emitir eventos
- **Eventos**: `PullEvents()` en el agregado los entrega y los limpia; el caso de uso los pasa al `EventPublisher`
- **Idempotencia**: toda operación que mueve dinero acepta una `idempotency_key`
- **Tests**: el dominio y la capa de application son testeables sin base de datos ni NATS
