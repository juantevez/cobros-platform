# Reporting — Documentación técnico-funcional

> **Bounded Context:** `context/reporting`
> **Migraciones:** [`000013_reporting_init`](../../migrations/000013_reporting_init.up.sql),
> [`000034_reporting_rls_nullif`](../../migrations/000034_reporting_rls_nullif.up.sql),
> [`000035_reporting_payment_adjustments`](../../migrations/000035_reporting_payment_adjustments.up.sql)
> **Streams NATS consumidos:** `PAYMENT` (`payment.>`), `LEDGER` (`ledger.entry.>`), `DISPUTE` (`dispute.resolved.>`) — no publica eventos
> **Lee tablas de otros BCs:** `ledger_accounts` (Ledger)

---

## 1. Propósito

Reporting es el **read-model (CQRS)** del dashboard del comercio. No tiene agregados ni reglas de
negocio y no mueve dinero: consume eventos de otros contextos, guarda **hechos inmutables** y los
agrega al momento de consultar.

Responsabilidades:

1. Proyectar cada pago capturado como un hecho (`report_payment_fact`), y cada reembolso y
   contracargo como un ajuste (`report_payment_adjustment`).
2. Proyectar cada posting de un asiento del Ledger, incluidas las reversas, como un movimiento
   (`report_ledger_movement`), con el tipo de cuenta denormalizado.
3. Responder tres consultas del tenant autenticado: volumen transaccional por período, comisiones
   (revenue) y saldos por tipo de cuenta.

### Fuera de alcance (hoy)

- Payouts y pagos fallidos: no se proyectan.
- Reportes de plataforma (cross-tenant) y exportación (CSV, PDF).
- Saldos a una fecha de corte: los saldos son siempre el acumulado total.

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Hecho** (*fact*) | Fila inmutable que representa algo que ya ocurrió (un pago capturado, un posting). Nunca se actualiza. |
| **Proyección** | Traducción de un evento de dominio a hechos del read-model. |
| **Bucket** | Intervalo de la serie de volumen: día, semana o mes (`date_trunc`). |
| **Volumen** | Cantidad de pagos y monto bruto capturado, por bucket y moneda. |
| **Ajuste** | Dinero que sale de un pago ya capturado: un **reembolso** (`payment.refunded.v1`) o un **contracargo** (disputa perdida, aceptada o vencida). |
| **Revenue** | Bruto capturado, comisiones, reembolsos, contracargos y neto (bruto − reembolsos − contracargos), por moneda y período. |
| **Saldo** (*balance*) | Débitos, créditos y neto por tipo de cuenta y moneda, sumando todos los movimientos del tenant. |

---

## 3. Flujo funcional

```mermaid
flowchart TD
    P[Payment: payment.captured.v1 y refunded.v1] --> C1[reporting-payment-consumer]
    D[Dispute: dispute.resolved.v1] --> C3[reporting-dispute-consumer]
    L[Ledger: ledger.entry.posted.v1 y entry.reversed.v1] --> C2[reporting-ledger-consumer]
    C1 --> UC[ProjectEventsUseCase]
    C2 --> UC
    C3 --> UC
    UC -->|tipo de cuenta por posting| AR[(ledger_accounts)]
    UC -->|INSERT ... ON CONFLICT DO NOTHING| F[(report_payment_fact)]
    UC -->|INSERT ... ON CONFLICT DO NOTHING| M[(report_ledger_movement)]
    UC -->|INSERT ... ON CONFLICT DO NOTHING| A[(report_payment_adjustment)]
    API[GET /api/v1/reports/*] --> Q[GetReportsUseCase]
    Q -->|SUM, COUNT, date_trunc| F
    Q -->|reembolsos y contracargos| A
    Q -->|SUM FILTER por dirección| M
```

- **Escritura (worker):** tres consumers durables. Cada evento se traduce a hechos y se inserta
  de forma idempotente.
- **Reversas:** el Ledger no emite `entry.posted` para una reversa sino `ledger.entry.reversed.v1`,
  que trae los postings de la reversa (los del original con la dirección invertida). Se proyectan
  como cualquier asiento, bajo el `reverse_entry_id`, y compensan al original en los saldos.
- **Lectura (API):** las métricas se calculan en cada consulta; no hay contadores precalculados.

### Reembolsos y contracargos

- **Reembolso:** total y único por pago (Payment pasa `captured` → `refunded` una sola vez), con
  el monto del evento. Clave: el `payment_id`.
- **Contracargo:** `dispute.resolved.v1` con resultado `lost`, `accepted` o `expired`. Clave: el
  `dispute_id`. Una disputa ganada no mueve dinero y no genera hecho. Un resultado desconocido se
  descarta como hecho inválido.
- Cada ajuste cuenta en el período en que ocurrió: un reembolso de octubre de un pago de
  septiembre resta en octubre.
- Las comisiones no se ajustan: ningún contexto modela hoy la devolución de comisiones.

### Por qué hechos y no contadores

JetStream entrega *al menos una vez*. Un contador incremental se duplicaría con cada re-entrega.
Un hecho con clave natural (`payment_id`, o `entry_id + account_id + direction`) se inserta con
`ON CONFLICT DO NOTHING`: proyectar dos veces el mismo evento no cambia nada.

### Fecha de los hechos

`captured_at` y `posted_at` toman el header `occurred-at` que agrega el relay del outbox: la hora
en que ocurrió el hecho, no la hora de proyección. Así una re-entrega tardía o un replay agregan
en el período correcto. Si el header falta o es inválido, se usa el reloj del worker.

---

## 4. Modelo de dominio

### 4.1 Hechos (`facts.go`)

| Hecho | Origen | Clave natural | Campos |
|---|---|---|---|
| `PaymentFact` | `payment.captured.v1` | `payment_id` | `tenant_id`, `currency`, `amount` (bruto), `platform_fee`, `psp_fee`, `payment_method`, `captured_at` |
| `LedgerMovement` | `ledger.entry.posted.v1` y `ledger.entry.reversed.v1`, un hecho por posting | `(entry_id, account_id, direction)` (en una reversa, `entry_id` = `reverse_entry_id`) | `tenant_id`, `account_type`, `currency`, `amount` (siempre > 0), `posted_at` |
| `PaymentAdjustment` | `payment.refunded.v1` (`refund`), `dispute.resolved.v1` perdida/aceptada/vencida (`chargeback`) | `(kind, source_id)`: `payment_id` o `dispute_id` | `payment_id`, `tenant_id`, `currency`, `amount`, `occurred_at` |

Montos en centavos.

### 4.2 Vistas (`views.go`)

| Vista | Campos |
|---|---|
| `VolumePoint` | `bucket`, `currency`, `payment_count`, `gross_amount` |
| `RevenueSummary` | `currency`, `payment_count`, `gross_amount`, `platform_fees`, `psp_fees`, `refund_count`, `refunded_amount`, `chargeback_count`, `chargeback_amount`, `net_amount` |
| `TenantBalance` | `account_type`, `currency`, `debits`, `credits`, `net` = `debits - credits` |

`Granularity`: `day` (default si viene vacío), `week`, `month`. `ParseGranularity` valida.

### 4.3 Errores

| Error | Uso |
|---|---|
| `ErrInvalidFact` | Evento sin `tenant_id` o sin ID, o disputa con resultado desconocido. Es permanente: el consumer lo descarta (ack) y lo loguea. |
| `ErrInvalidRange` | `from` posterior a `to` → 400. |

---

## 5. Casos de uso

| Caso de uso | Qué hace |
|---|---|
| `ProjectEventsUseCase.ProjectPaymentCaptured` | Valida `payment_id` y `tenant_id` y guarda el `PaymentFact`. |
| `ProjectEventsUseCase.ProjectEntryPosted` | Valida `entry_id` y `tenant_id`; por cada posting con cuenta, busca el tipo de cuenta en `ledger_accounts` (con el tenant del evento) y guarda el `LedgerMovement`. Una cuenta inexistente se proyecta con tipo vacío. Lo usan los asientos y las reversas. |
| `ProjectEventsUseCase.ProjectPaymentRefunded` | Valida `payment_id` y `tenant_id` y guarda el ajuste `refund`. |
| `ProjectEventsUseCase.ProjectDisputeResolved` | Valida IDs y tenant; si el resultado es `lost`/`accepted`/`expired` guarda el ajuste `chargeback`; si es `won`, nada. |
| `GetReportsUseCase.GetVolume` | Valida el rango y delega la serie agregada (bruto capturado). |
| `GetReportsUseCase.GetRevenue` | Valida el rango y delega el resumen por moneda: capturas del período y ajustes del período, unidos por moneda (una moneda con solo ajustes aparece con bruto cero). |
| `GetReportsUseCase.GetBalances` | Delega los saldos del tenant (sin rango). |

Los postings de un asiento se insertan uno por uno, sin transacción. Si uno falla, el evento se
reintenta completo; los que ya estaban se ignoran por la clave natural.

---

## 6. API HTTP

Grupo `protected` (JWT) → `/api/v1/reports`. El tenant sale del token. Todas las rutas exigen el
permiso `reports:read` (`RouteGuards.Read`, inyectado desde `cmd/api`; `RegisterRoutes` entra en
pánico sin él): sin permiso → 403. Un token sin tenant (p. ej. un usuario de plataforma) → 403
`tenant required`.

| Método | Ruta | Respuesta | Códigos |
|---|---|---|---|
| `GET` | `/volume?from=&to=&granularity=day` | `{granularity, points:[VolumePoint]}` | 200 · 400 · 401 · 403 · 500 |
| `GET` | `/revenue?from=&to=` | `{revenue:[RevenueSummary]}` (bruto, comisiones, reembolsos, contracargos, neto) | 200 · 400 · 401 · 403 · 500 |
| `GET` | `/balances` | `{balances:[TenantBalance]}` | 200 · 401 · 403 · 500 |

- `from` y `to` en RFC3339, opcionales. Rango semiabierto `[from, to)`: períodos contiguos no
  cuentan dos veces un hecho del límite. Sin `from` o sin `to`, ese extremo queda abierto.
- `granularity`: `day`, `week` o `month`; otro valor → 400.
- Los buckets se calculan con `date_trunc` en la zona horaria de la sesión de Postgres (UTC).

---

## 7. Eventos

### 7.1 Consumidos

| Consumer | Stream | Filtro | Proyección |
|---|---|---|---|
| `reporting-payment-consumer` | `PAYMENT` | `payment.>` | `payment.captured.v1` → `PaymentFact`; `payment.refunded.v1` → ajuste `refund` |
| `reporting-ledger-consumer` | `LEDGER` | `ledger.entry.>` | `ledger.entry.posted.v1` y `ledger.entry.reversed.v1` → `LedgerMovement` por posting |
| `reporting-dispute-consumer` | `DISPUTE` | `dispute.resolved.>` | `dispute.resolved.v1` perdida/aceptada/vencida → ajuste `chargeback` |

`MaxDeliver: 5`. Semántica de ack:

| Situación | Resultado |
|---|---|
| Proyección OK, o hecho ya existente | ack |
| Payload ilegible o sin `tenant_id` | ack, sin log |
| `ErrInvalidFact` (falta el ID, resultado de disputa desconocido) | ack + warn |
| Disputa ganada | ack, sin hecho |
| Reversa sin postings (publicada antes de que el evento los llevara) | ack + warn: no se proyecta |
| Error de base u otro transitorio | nak → reintento |
| Otro subject del filtro | ack silencioso |

### 7.2 Publicados

Ninguno.

---

## 8. Persistencia

Migración `000013_reporting_init`:

| Tabla | Clave | Índice |
|---|---|---|
| `report_payment_fact` | PK `payment_id` | `(tenant_id, captured_at)` |
| `report_ledger_movement` | PK `(entry_id, account_id, direction)`; `CHECK direction IN ('debit','credit')` | `(tenant_id, account_type, currency)` |
| `report_payment_adjustment` (`000035`) | PK `(kind, source_id)`; `CHECK kind IN ('refund','chargeback')` | `(tenant_id, occurred_at)` |

Las tres tienen RLS por `tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid`
(`000034`, `000035`). Cada método de los repositorios y del `AccountReader` corre en una sesión con
`app.current_tenant` fijado (`withTenant` → `RunWithAppTenant`): las escrituras con el tenant del
hecho y las consultas con el del token.

---

## 9. Seguridad y multi-tenancy

- **Autenticación:** JWT (grupo `protected`). Las API keys no llegan a estas rutas.
- **Autorización:** permiso `reports:read` (`admin`, `accountant`, `read_only`,
  `platform_support`) o el scope `reports:read` de una API key. `operator` no accede.
- **Aislamiento:** el `WHERE tenant_id = $1` explícito de cada consulta y la RLS de las tablas del
  read-model y de `ledger_accounts`. Verificado con un rol sin `BYPASSRLS`: la proyección inserta,
  el tipo de cuenta se resuelve y un tenant no ve los datos de otro.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Severidad | Tema | Detalle |
|---|---|---|---|
| 1 | Media | Signo del neto | `net = debits - credits`; el saldo del Ledger es `credits - debits`, así que el mismo saldo sale con signo opuesto en `/ledger` y en `/reports/balances`. El neto de Reporting es coherente con cómo asientan los consumers del Ledger (sobre `merchant_balance`, un pago capturado es débito y un payout es crédito): el que está al revés es el saldo del Ledger, y por eso Payout rechazaría todo payout ([LEDGER.md §10](LEDGER.md) #2). Se resuelve en el Ledger; después Reporting expone el neto según la naturaleza de cada cuenta. |
| 2 | Baja | Zona horaria | Los buckets de `day`/`week`/`month` se cortan en UTC; para un comercio en Argentina el "día" empieza a las 21:00. No hay parámetro de zona. |
| 3 | Baja | Descartes silenciosos | Un payload ilegible o sin tenant se descarta sin log. Una cuenta que no se encuentra se proyecta con `account_type` vacío sin aviso, y su saldo aparece bajo un tipo vacío. |
| 4 | Baja | Consultas sin tope | Sin `from`/`to` se agrega todo el histórico; con `granularity=day` sobre años, la serie no tiene límite de puntos. |
| 5 | Baja | Comentarios desactualizados | La migración describe `captured_at`/`posted_at` como "momento de proyección"; hoy son la hora del evento. |
| 6 | Producto | Alcance | Saldos sin fecha de corte. Sin métricas de payouts, pagos fallidos ni tasa de aprobación. Sin reportes de plataforma ni exportación. |

---

## 11. Testing

`go test ./context/reporting/...` (100 % de cobertura de sentencias en los cinco paquetes).

| Paquete | Enfoque |
|---|---|
| `domain` | `ParseGranularity` (vacío → `day`, valores inválidos), contrato JSON de las vistas (incluidos ajustes y neto), tipos de ajuste, errores. |
| `application` | Proyección con fakes: validación de IDs, fecha del hecho desde el evento o el reloj, tipo de cuenta por posting con el tenant del evento, postings sin cuenta, reembolsos, contracargos por resultado de disputa (y ganada sin hecho), errores del writer y del lector; consultas con validación de rango. |
| `http` | Parseo de `from`/`to` y `granularity`, 400 por formato o rango, 403 sin tenant, 500 sin detalle interno, guard en cada ruta y pánico sin guard. |
| `nats` | Traducción de cada payload (incluidos la reversa bajo `reverse_entry_id`, el reembolso y la disputa), configuración de los tres consumers, header `occurred-at`, ack/nak según el error, reversa sin postings, subjects ajenos. |
| `postgres` | pgxmock: SQL idempotente de cada inserción y cada consulta dentro de la sesión RLS, error al fijar la sesión, armado del `WHERE` con rango semiabierto, revenue con capturas y ajustes por moneda, agregaciones y cálculo de los netos. |

La RLS se verificó además contra Postgres con un rol sin `BYPASSRLS`: proyección de un pago (con
re-entrega), un asiento y su reversa, tipo de cuenta resuelto, saldos compensados y aislamiento
entre tenants; y el revenue con reembolsos, contracargos (por período), re-entregas, disputa
ganada y una moneda con solo ajustes. Esas pruebas no están automatizadas.

---

## 12. Mapa de código

```
context/reporting/
├── domain/
│   ├── facts.go            # PaymentFact, LedgerMovement, PaymentAdjustment (hechos inmutables)
│   ├── views.go            # Granularity, VolumePoint, RevenueSummary, TenantBalance
│   └── errors.go           # ErrInvalidFact, ErrInvalidRange
├── application/
│   ├── ports.go            # ProjectionWriter, AccountTypeReader, ReportReader, queries
│   ├── project_events.go   # ProjectEventsUseCase: pagos, asientos, reembolsos, contracargos (+ Clock)
│   └── get_reports.go      # GetReportsUseCase
└── infrastructure/adapters/
    ├── inbound/http/handler.go          # 3 endpoints, RouteGuards, RegisterRoutes
    ├── inbound/nats/event_consumer.go   # 3 consumers (pagos y reembolsos; asientos y reversas; disputas), ack/nak
    └── outbound/postgres/
        ├── projection_repo.go           # inserciones idempotentes
        ├── report_repo.go               # agregaciones (Volume, Revenue, Balances)
        ├── account_reader.go            # tipo de cuenta desde ledger_accounts
        └── rls.go                       # sesión RLS (withTenant)
```

Wiring: `cmd/api/main.go` (consultas, guard `reports:read` y rutas) y `cmd/worker/main.go`
(proyección y consumers).
