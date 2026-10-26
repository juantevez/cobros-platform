# Ledger (Libro Mayor) — Documentación técnico-funcional

> **Bounded Context:** `context/ledger`
> **Migraciones:** [`000003_ledger_init`](../../migrations/000003_ledger_init.up.sql), ajuste del `CHECK` de tipos en [`000007_payout_init`](../../migrations/000007_payout_init.up.sql)
> **Stream NATS:** `LEDGER` (`ledger.>`)

---

## 1. Propósito

Ledger es el **libro mayor de doble partida** de la plataforma y la **fuente de verdad del dinero**. Registra cada hecho económico (pago capturado, desembolso, disputa) como un **asiento** inmutable compuesto por **postings** que balancean, y mantiene el **saldo** de cada cuenta.

Principios de diseño:

| Principio | Cómo se implementa |
|---|---|
| Doble partida | Validada en el dominio (`validateBalance`) y reforzada en la base por un *constraint trigger* diferido. |
| Append-only | No hay `UPDATE` ni `DELETE` de asientos ni postings: los errores se corrigen con un **asiento de reversa**. |
| Idempotencia | `UNIQUE (tenant_id, idempotency_key)` en `journal_entries` + chequeo previo en el caso de uso. |
| Saldos consistentes | `account_balances` se actualiza en la **misma transacción** que el asiento. |
| Dinero sin flotantes | `int64` en unidades mínimas (centavos) + código ISO 4217. |

Ledger es mayormente **reactivo**: la mayoría de los asientos los generan sus consumers a partir de eventos de Onboarding, Payment, Payout y Dispute. También expone una API HTTP (§6).

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Cuenta** (`Account`) | Cuenta contable de un tenant, con un tipo y una moneda. Única por `(tenant, tipo, moneda)`. |
| **Asiento** (`JournalEntry`) | Hecho económico completo: ≥ 2 postings que balancean. Agregado raíz. |
| **Posting** | Línea de un asiento: cuenta + dirección (`debit`/`credit`) + monto > 0. |
| **Saldo** | Proyección acumulada por cuenta en `account_balances`. |
| **Clave de idempotencia** | Identifica unívocamente un asiento dentro del tenant (p. ej. `payment_captured_<id>`). |
| **Reversa** | Asiento que invierte débitos↔créditos de otro, dejando el efecto neto en cero. |

### 2.1 Tipos de cuenta

| Tipo | Uso previsto | ¿Quién la crea? |
|---|---|---|
| `merchant_balance` | Saldo disponible del comercio (lo que la plataforma le debe). | Consumer de Onboarding |
| `reserve` | Reserva de garantía (*rolling reserve*). | Consumer de Onboarding (sin movimientos hoy) |
| `dispute_hold` | Fondos congelados por disputas. | Consumer de Onboarding |
| `payout_transit` | Fondos en camino al banco del comercio. | Consumer de Onboarding |
| `payout_sent` | Fondos confirmados como enviados. | Consumer de Onboarding |
| `in_transit` | Fondos cobrados que el PSP aún no liquidó. | ⚠️ **Nadie** (§10.1) |
| `platform_fees` | Comisiones de la plataforma (y hoy, contrapartida de disputas perdidas). | ⚠️ **Nadie** (§10.1) |

---

## 3. Flujo funcional

```mermaid
sequenceDiagram
    autonumber
    participant OB as Onboarding
    participant PAY as Payment
    participant PO as Payout
    participant DS as Dispute
    participant N as NATS
    participant L as Ledger (worker)
    participant DB as PostgreSQL

    OB-->>N: onboarding.application.approved.v1
    N-->>L: ledger-onboarding-consumer
    L->>DB: 5 × CreateAccount (merchant_balance, reserve, dispute_hold, payout_transit, payout_sent)

    PAY-->>N: payment.captured.v1
    N-->>L: ledger-payment-consumer
    L->>DB: PostEntry payment_captured_<id> (in_transit / merchant_balance / platform_fees)

    PO->>DB: lee account_balances (merchant_balance) para validar el saldo
    PO-->>N: payout.initiated | confirmed | failed
    N-->>L: ledger-payout-consumer
    L->>DB: PostEntry payout_*_<id>

    DS-->>N: dispute.opened | resolved
    N-->>L: ledger-dispute-consumer
    L->>DB: PostEntry dispute_*_<id>

    L-->>N: ledger.entry.posted.v1 → Reporting, Audit
```

---

## 4. Modelo de dominio

### 4.1 `Money`

Archivo: [domain/vo.go](../../context/ledger/domain/vo.go)

- `amount int64 ≥ 0` en unidades mínimas; `currency` de 3 letras, normalizada a mayúsculas.
- El sentido del movimiento **no** se expresa con el signo sino con `Direction`.
- `Add` falla si las monedas difieren (`ErrInvalidMoneyOp`).

### 4.2 Agregado `Account`

Archivo: [domain/account.go](../../context/ledger/domain/account.go)

Solo creación: no se modifica ni se elimina. Valida la moneda y la **normaliza a mayúsculas** (`"ars"` → `"ARS"`), para que coincida con la de los postings. Emite `ledger.account.created.v1`. Al persistirla se inicializa su fila en `account_balances` con saldo 0.

### 4.3 Agregado `JournalEntry` y `Posting`

Archivo: [domain/entry.go](../../context/ledger/domain/entry.go)

Invariantes de `NewJournalEntry`:

1. Al menos **2 postings** (`ErrNotEnoughPostings`).
2. Todos con la **misma moneda** (`ErrCurrencyMismatch`).
3. Todo monto **> 0** (`ErrZeroAmount`).
4. **Σ débitos = Σ créditos** (`ErrEntryNotBalanced`).

Al crearse emite `ledger.entry.posted.v1` con todos sus postings.

`BuildReverse(reverseID, key)` genera un asiento con cada posting invertido, descripción `"Reversa de: …"` y metadata `original_entry_id`. **Reemplaza** el evento `entry.posted` por `ledger.entry.reversed.v1` (ver §10.6).

El agregado no conoce las cuentas. Que existan, pertenezcan al tenant y tengan la moneda del posting lo valida `PostEntry` antes de construirlo (§5.1).

### 4.4 Convención de saldos

Archivo: [balance_repo.go](../../context/ledger/infrastructure/adapters/outbound/postgres/balance_repo.go)

`Apply` aplica la misma regla a **todas** las cuentas, sin importar su tipo:

```
credit → balance += amount
debit  → balance -= amount
```

El comentario del repositorio declara que *"merchant_balance positivo = la plataforma le debe ese monto al comercio"*, y Payout se apoya en eso. Los consumers, en cambio, **debitan** `merchant_balance` para aumentarlo. Ver §10.3.

### 4.5 Errores

Archivo: [domain/errors.go](../../context/ledger/domain/errors.go)

| Error | HTTP |
|---|---|
| `ErrAccountNotFound` (incluye cuentas de otro tenant), `ErrEntryNotFound` | 404 |
| `ErrEntryNotBalanced`, `ErrCurrencyMismatch`, `ErrAccountCurrencyMismatch`, `ErrNotEnoughPostings`, `ErrZeroAmount`, `ErrInvalidDirection`, `ErrInvalidAccountType`, `ErrInvalidCurrency`, `ErrNegativeAmount` | 422 |
| `ErrEntryAlreadyExists` | 409 (declarado pero **nunca devuelto**) |
| `ErrAccountAlreadyExists` | 409 (en la práctica no llega al HTTP: `CreateAccount` la resuelve devolviendo la cuenta existente) |
| Otros: IDs inválidos, violación de `UNIQUE` de asientos o del trigger, cuenta inexistente en `Apply` | 500 |

---

## 5. Casos de uso

Directorio: [application/](../../context/ledger/application/)

| Caso de uso | Qué hace | Evento |
|---|---|---|
| `CreateAccount` | **Idempotente** por `(tenant, tipo, moneda)`: si ya existe devuelve su ID con `WasExisting=true`, sin evento. Si no existe, crea la cuenta y su saldo en 0 en una transacción. Ante una carrera, el `UNIQUE` → `ErrAccountAlreadyExists` → se relee la cuenta y se devuelve. | `ledger.account.created.v1` (solo si la crea) |
| `PostEntry` | Valida, chequea idempotencia y en **una transacción** guarda el asiento + postings, aplica los saldos y publica en el outbox. | `ledger.entry.posted.v1` |
| `ReverseEntry` | Carga el original, verifica el tenant y la idempotencia (`reverse_<clave original>`), y persiste la reversa igual que `PostEntry`. | `ledger.entry.reversed.v1` |
| `GetBalance` | Devuelve el saldo y la moneda de una cuenta **del tenant**. Una cuenta de otro tenant → `ErrAccountNotFound`. | — |

### 5.1 `PostEntry` en detalle

```
1. idempotency_key no vacía, ≥ 2 líneas, tenant_id válido
2. FindByIdempotencyKey(tenant, key)
     └─ existe → devuelve {entry_id, was_existing: true}   (sin comparar contenido)
3. Parsear líneas → validateAccounts: cada cuenta existe, es del tenant
   (si no → ErrAccountNotFound) y su moneda = la del posting (ErrAccountCurrencyMismatch)
   → NewJournalEntry (invariantes §4.3)
4. RunInTx {
     entryRepo.Save      → journal_entries + postings
     balanceRepo.Apply   → UPDATE account_balances SET balance = balance ± amount
     publisher.Publish   → outbox_messages
   }   // al COMMIT corre el trigger diferido check_entry_balance
```

- Si `occurred_at` viene vacío se usa el reloj. El HTTP siempre lo completa.
- La idempotencia es **check-then-insert**: dos requests concurrentes con la misma clave pasan el chequeo, y la segunda falla con una violación de `UNIQUE` → **500** en lugar de devolver el asiento existente.
- Reusar una clave con **otro contenido** devuelve el asiento original sin avisar.

---

## 6. API HTTP

Directorio: [infrastructure/adapters/inbound/http/](../../context/ledger/infrastructure/adapters/inbound/http/)

Rutas bajo `/api/v1/ledger`, protegidas por JWT. El tenant sale del token. Las rutas de **escritura** pasan además por un `writeGuard` que `cmd/api` inyecta como `RequireRole(platform_support)`, así Ledger no importa Auth. `RegisterRoutes` entra en pánico si el guard es `nil`.

| Método | Ruta | Rol | Éxito |
|---|---|---|---|
| `POST` | `/ledger/accounts` | `platform_support` | `201 {"account_id"}` nueva / `200` si ya existía |
| `GET` | `/ledger/accounts/:accountID/balance` | cualquiera (solo cuentas propias) | `200 {"account_id","balance","currency"}` |
| `POST` | `/ledger/entries` | `platform_support` | `201` nuevo / `200` si la clave ya existía: `{"entry_id","was_existing"}` |
| `POST` | `/ledger/entries/:entryID/reverse` | `platform_support` | `201 {"reverse_entry_id"}` (también si la reversa ya existía) |

Sin el rol → `403 insufficient permissions`. Como el tenant sale del token y `PostEntry` valida el tenant de cada cuenta, un usuario `platform_support` solo puede postear sobre las cuentas **de su propio tenant**. Los ajustes manuales sobre un comercio requieren un endpoint de operador específico, que hoy no existe.

### 6.1 Crear cuenta

```json
{ "account_type": "merchant_balance", "currency": "ARS", "description": "Saldo del comercio" }
```

Si la cuenta ya existe para `(tenant, tipo, moneda)` → `200` con el `account_id` existente.

### 6.2 Registrar asiento

```json
{
  "idempotency_key": "manual_adj_2026_10_001",
  "description": "Ajuste manual",
  "occurred_at": "2026-10-26T12:00:00Z",
  "metadata": { "ticket": "OPS-123" },
  "lines": [
    { "account_id": "…", "direction": "debit",  "amount": 15000, "currency": "ARS" },
    { "account_id": "…", "direction": "credit", "amount": 15000, "currency": "ARS" }
  ]
}
```

Binding HTTP: `idempotency_key` requerido, `lines` mínimo 2, y cada línea con `account_id`, `direction`, `amount ≥ 1` y `currency`. Una cuenta inexistente o de otro tenant → `404`. Una moneda distinta a la de la cuenta → `422`.

### 6.3 Saldo

`GET /ledger/accounts/:accountID/balance` → `{"account_id":"…","balance":-12500,"currency":"ARS"}`. El saldo puede ser negativo: no hay control de sobregiro.

---

## 7. Eventos

### 7.1 Publicados (stream `LEDGER`)

| Subject | Payload | Consumidores |
|---|---|---|
| `ledger.account.created.v1` | `account_id`, `tenant_id`, `account_type`, `currency` | Audit |
| `ledger.entry.posted.v1` | `entry_id`, `tenant_id`, `idempotency_key`, `description`, `postings[]{account_id, direction, amount, currency}` | Audit, **Reporting** (proyección de saldos por comercio) |
| `ledger.entry.reversed.v1` | `reverse_entry_id`, `original_entry_id`, `tenant_id` | Audit |

Los headers son los estándar del Outbox (ver ONBOARDING.MD §7).

### 7.2 Consumidos

Todos corren en `cmd/worker` con `MaxDeliver: 5`. Si fallan los 5 intentos, el mensaje se descarta y no queda registro del asiento.

| Durable | Filtro | Asiento (según el código) | Clave de idempotencia |
|---|---|---|---|
| `ledger-onboarding-consumer` | `onboarding.application.approved.v1` | Crea 5 cuentas (§2.1) en `currency` (default `ARS`). | — (no es un asiento) |
| `ledger-payment-consumer` | `payment.captured.v1` | `in_transit` **C** amount · `merchant_balance` **D** (amount − platform_fee − psp_fee) · `platform_fees` **D** platform_fee (si existe la cuenta) | `payment_captured_<payment_id>` |
| `ledger-payout-consumer` | `payout.>` | **initiated:** `merchant_balance` C · `payout_transit` D. **confirmed:** `payout_transit` C · `payout_sent` D. **failed:** `payout_transit` C · `merchant_balance` D | evento `idempotency_key` o `payout_initiated_<id>`; `payout_confirmed_<id>`; `payout_failed_<id>` |
| `ledger-dispute-consumer` | `dispute.>` | **opened:** `merchant_balance` C · `dispute_hold` D. **resolved won:** `dispute_hold` C · `merchant_balance` D. **resolved lost/accepted/expired:** `dispute_hold` C · `platform_fees` D | `dispute_opened_<id>`, `dispute_resolved_<id>` |

C = crédito (suma al saldo), D = débito (resta del saldo), según `Apply`.

### 7.3 Lecturas directas de otros BCs

[`payout/.../outbound/ledger/balance_checker.go`](../../context/payout/infrastructure/adapters/outbound/ledger/balance_checker.go) consulta `account_balances` ⨝ `ledger_accounts` (tipo `merchant_balance`) para validar el saldo antes de iniciar un payout: exige **saldo > 0 y ≥ monto**. Esto acopla Payout al esquema y a la **convención de signo** de Ledger.

---

## 8. Persistencia

```mermaid
erDiagram
    ledger_accounts ||--|| account_balances : "account_id"
    journal_entries ||--|{ postings : "entry_id"
    ledger_accounts ||--o{ postings : "account_id"

    ledger_accounts { uuid id PK
                      uuid tenant_id
                      text type
                      char3 currency
                      text description }
    account_balances { uuid account_id PK,FK
                       bigint balance
                       timestamptz updated_at }
    journal_entries { uuid id PK
                      uuid tenant_id
                      text idempotency_key
                      text description
                      jsonb metadata
                      timestamptz occurred_at }
    postings { uuid id PK
               uuid entry_id FK
               uuid tenant_id
               uuid account_id FK
               text direction
               bigint amount
               char3 currency }
```

| Tabla | Restricciones clave | RLS |
|---|---|---|
| `ledger_accounts` | `UNIQUE (tenant_id, type, currency)`; `CHECK type IN (…7 tipos)` desde la migración 000007 | Sí |
| `account_balances` | PK = `account_id`; `balance BIGINT` (puede ser negativo) | **No** (no tiene `tenant_id`) |
| `journal_entries` | `UNIQUE (tenant_id, idempotency_key)`; índice `(tenant_id, created_at DESC)` | Sí |
| `postings` | `amount > 0`; `direction IN ('debit','credit')`; FK a la cuenta (sin chequear el tenant) | Sí |

**Trigger `trg_check_entry_balance`**: `CONSTRAINT TRIGGER … AFTER INSERT ON postings DEFERRABLE INITIALLY DEFERRED`. Al hacer COMMIT, por cada posting insertado recalcula Σ débitos y Σ créditos del asiento, y aborta la transacción si hay ≥ 2 postings y no balancean. Es la segunda línea de defensa, por si alguien inserta por fuera del dominio.

Recordatorio: la app conecta como owner, así que el RLS no filtra (ver ONBOARDING.MD §10.2). Todo el aislamiento depende de las queries.

---

## 9. Concurrencia

- `UPDATE account_balances SET balance = balance + $delta` es atómico por fila: no se pierden actualizaciones.
- Los postings se aplican en el orden en que llegan, sin ordenar por `account_id`. Dos asientos concurrentes que tocan las mismas cuentas en distinto orden pueden generar un **deadlock**. Postgres aborta uno; el consumer reintenta, y el HTTP devuelve 500.
- Las lecturas de idempotencia y del asiento original (en la reversa) se hacen **fuera** de la transacción.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Tema | Detalle | Impacto |
|---|---|---|---|
| 1 | **Los pagos capturados nunca llegan al Ledger** | (a) Ningún flujo crea las cuentas `in_transit` ni `platform_fees`: el consumer de Onboarding crea las otras 5. `FindByTenantAndType(in_transit)` falla y el mensaje se descarta tras 5 intentos. (b) Aunque existieran, el asiento acredita `amount` y debita `amount − platform_fee − psp_fee` + `platform_fee` = `amount − psp_fee`: **no balancea si `psp_fee > 0`**, y el PSP mock cobra siempre ≥ 1. El comentario *"el asiento balancea igual"* del consumer es incorrecto. Falta una línea para `psp_fee`. (c) Las disputas perdidas también fallan por falta de `platform_fees`. | **Crítico (funcional)** |
| 2 | ~~Un comercio puede crear saldo~~ ✅ **Resuelto** | Las escrituras HTTP exigen `platform_support` (`writeGuard`). `PostEntry` valida que cada cuenta exista, sea del tenant y tenga la moneda del posting. `GetBalance` verifica el tenant. Pendiente: un endpoint de operador para ajustes manuales sobre otro tenant (§6). | — |
| 3 | **Convención de signo inconsistente** | `Apply` suma créditos y resta débitos en todas las cuentas. Los consumers **debitan** `merchant_balance` cuando el comercio gana dinero (pago, disputa ganada) y lo **acreditan** cuando sale (payout, disputa abierta). Con #1 resuelto, cada pago dejaría `merchant_balance` **más negativo**, y Payout (que exige saldo > 0) rechazaría todos los payouts. Se puede arreglar con saldo por naturaleza de cuenta (deudora/acreedora) o invirtiendo las direcciones en los consumers. Hay que alinearlo con Reporting. | **Alto (funcional)** |
| 4 | ~~Creación de cuentas no idempotente~~ ✅ **Resuelto** | `CreateAccount` devuelve la cuenta existente (chequeo previo + traducción de `23505` a `ErrAccountAlreadyExists` ante carreras). Los reintentos del consumer de Onboarding completan las cuentas faltantes. La moneda se normaliza a mayúsculas para que la búsqueda coincida. | — |
| 5 | Idempotencia con carrera | Check-then-insert: un duplicado concurrente → 500 en lugar de `was_existing`. No se compara el contenido al reusar una clave. `ErrEntryAlreadyExists` nunca se emite. | Medio |
| 6 | La reversa no llega a Reporting | `BuildReverse` reemplaza `entry.posted` por `entry.reversed`, que no trae postings, y Reporting solo escucha `entry.posted`: sus saldos no reflejan las reversas. Además, Audit lee `p.EntryID` en `entry.reversed`, pero ese evento no tiene `entry_id`, así que el `ResourceID` queda vacío. | Medio |
| 7 | `account_balances` sin `tenant_id` ni RLS | Depende de que cada query haga join con `ledger_accounts` filtrando por tenant, o valide la cuenta antes (Payout hace el join; `GetBalance` y `PostEntry` validan la cuenta). | Bajo |
| 8 | Posible deadlock | Los postings no se ordenan por `account_id` antes de `Apply` (§9). | Bajo |
| 9 | Sin control de sobregiro | Ninguna cuenta impide un saldo negativo, ni siquiera `merchant_balance` ante un payout concurrente que pase el chequeo de Payout dos veces. | Medio |
| 10 | Cuentas de plataforma por tenant | `platform_fees` es por tenant, y se usa como contrapartida de disputas perdidas (marcado como provisorio: "Fase 4: chargebacks_paid"). No hay cuentas de la plataforma (revenue consolidado, banco del PSP). `reserve` no tiene movimientos. | Diseño |
| 11 | Errores técnicos → 500 | IDs inválidos, cuenta inexistente en `Apply`, violaciones de `UNIQUE`. | Bajo |
| 12 | Orden de postings no preservado | Se cargan con `ORDER BY id` (UUID aleatorio). | Bajo |
| 13 | Sin consultas | No hay listado de cuentas, de asientos ni extracto por cuenta. | Funcional |

---

## 11. Testing

| Capa | Archivos | Tipo |
|---|---|---|
| Dominio | `domain/*_test.go` | `Money`, invariantes de `JournalEntry`, `BuildReverse`, eventos. |
| Aplicación | `application/*_test.go` | `CreateAccount` (incluye idempotencia y carrera), `PostEntry` (idempotencia, cuenta inexistente, cuenta de otro tenant, moneda distinta a la de la cuenta), `GetBalance` (aislamiento), `ReverseEntry`. |
| HTTP | `inbound/http/*_test.go` | Handlers, router, `writeGuard` (403 en las escrituras; la lectura no se ve afectada), 404 cross-tenant y mapeo de errores. |
| NATS | `inbound/nats/*_test.go` | Los 4 consumers con repos y casos de uso falsos, más redelivery y reintento parcial del consumer de Onboarding. ⚠️ Los fakes no reproducen #1: tienen las cuentas precargadas y usan `psp_fee` que balancea. |
| Postgres | `outbound/postgres/*_test.go` | Integración contra PostgreSQL (`DATABASE_URL`), incluido el mapeo de `23505` → `ErrAccountAlreadyExists`; se saltan si no hay DB. |

```bash
go test ./context/ledger/...
```

Falta un test de integración de punta a punta: aprobar onboarding → capturar pago → iniciar payout. Ese test haría fallar #1 y #3.

---

## 12. Mapa de código

```
context/ledger/
├── domain/
│   ├── vo.go          # IDs, AccountType, Direction, Money
│   ├── account.go     # Agregado Account
│   ├── entry.go       # Agregado JournalEntry + Posting + BuildReverse + validateBalance
│   ├── events.go      # 3 eventos
│   └── errors.go
├── application/
│   ├── ports.go       # AccountRepository, EntryRepository, BalanceRepository, TxManager, EventPublisher, Clock
│   ├── commands.go
│   ├── create_account.go
│   ├── post_entry.go  # PostEntry + GetBalance + RealClock
│   └── reverse_entry.go
└── infrastructure/adapters/
    ├── inbound/http/  # router, account_handler, entry_handler, response
    ├── inbound/nats/  # onboarding, payment, payout, dispute consumers
    └── outbound/postgres/  # account_repo, entry_repo, balance_repo
```

Cableado:

- [cmd/api/main.go](../../cmd/api/main.go): sección `Ledger` (rutas HTTP).
- [cmd/worker/main.go](../../cmd/worker/main.go): los 4 consumers.
