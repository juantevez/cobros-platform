# Ledger (Libro Mayor) — Documentación técnico-funcional

> **Bounded Context:** `context/ledger`
> **Migraciones:** [`000003_ledger_init`](../../migrations/000003_ledger_init.up.sql), ajuste del `CHECK` de tipos en [`000007_payout_init`](../../migrations/000007_payout_init.up.sql), cuentas de plataforma faltantes en [`000036_ledger_platform_accounts_backfill`](../../migrations/000036_ledger_platform_accounts_backfill.up.sql), RLS en [`000037_ledger_rls_nullif`](../../migrations/000037_ledger_rls_nullif.up.sql), orden de postings en [`000038_ledger_posting_position`](../../migrations/000038_ledger_posting_position.up.sql), tenant de los saldos en [`000039_ledger_balances_tenant`](../../migrations/000039_ledger_balances_tenant.up.sql)
> **Stream NATS:** `LEDGER` (`ledger.>`)

---

## 1. Propósito

Ledger es el **libro mayor de doble partida** de la plataforma y la **fuente de verdad del dinero**. Registra cada hecho económico (pago capturado, reembolso, desembolso, disputa) como un **asiento** inmutable compuesto por **postings** que balancean, y mantiene el **saldo** de cada cuenta.

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
| `in_transit` | Fondos cobrados que el PSP aún no liquidó. Saldo negativo: lo que el PSP adeuda. | Consumer de Onboarding |
| `platform_fees` | Comisiones de la plataforma. | Consumer de Onboarding |

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
    L->>DB: 7 × CreateAccount (§2.1)

    PAY-->>N: payment.captured | refunded
    N-->>L: ledger-payment-consumer
    L->>DB: PostEntry payment_captured_<id> / payment_refunded_<id>

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

`BuildReverse(reverseID, key)` genera un asiento con cada posting invertido, descripción `"Reversa de: …"` y metadata `original_entry_id`. **Reemplaza** el evento `entry.posted` por `ledger.entry.reversed.v1`, que lleva los postings de la reversa para quien proyecta movimientos (Reporting).

El agregado no conoce las cuentas. Que existan, pertenezcan al tenant y tengan la moneda del posting lo valida `PostEntry` antes de construirlo (§5.1).

### 4.4 Convención de saldos

Archivo: [balance_repo.go](../../context/ledger/infrastructure/adapters/outbound/postgres/balance_repo.go)

`Apply` aplica la misma regla a **todas** las cuentas, sin importar su tipo:

```
credit → balance += amount
debit  → balance -= amount
```

Los consumers asientan con las direcciones de la contabilidad estándar, así que el saldo se lee igual en todas las cuentas de pasivo e ingreso:

| Cuenta | Saldo positivo significa | Sube con |
|---|---|---|
| `merchant_balance` | Lo que la plataforma le debe al comercio. Payout exige que sea > 0. | Pago capturado, disputa ganada, payout fallido |
| `platform_fees` | Comisiones ganadas. | Pago capturado |
| `dispute_hold` | Fondos congelados por disputas abiertas. | Disputa abierta |
| `payout_transit` | Payouts iniciados sin confirmar. | Payout iniciado |
| `payout_sent` | Total enviado a bancos. | Payout confirmado |

`in_transit` es la única cuenta de activo: queda **negativa** mientras el PSP adeuda la liquidación (un pago la debita; un reembolso o un contracargo la acreditan). Reporting calcula su neto con la misma regla.

### 4.5 Errores

Archivo: [domain/errors.go](../../context/ledger/domain/errors.go)

| Error | HTTP |
|---|---|
| `ErrInvalidID` (cuenta, asiento o tenant que no es UUID) | 400 |
| `ErrAccountNotFound` (incluye cuentas de otro tenant), `ErrEntryNotFound` | 404 |
| `ErrEntryNotBalanced`, `ErrCurrencyMismatch`, `ErrAccountCurrencyMismatch`, `ErrNotEnoughPostings`, `ErrZeroAmount`, `ErrInvalidDirection`, `ErrInvalidAccountType`, `ErrInvalidCurrency`, `ErrNegativeAmount`, `ErrIdempotencyKeyRequired` | 422 |
| `ErrIdempotencyKeyConflict` (clave ya usada con otros movimientos) | 409 |
| `ErrEntryAlreadyExists`, `ErrAccountAlreadyExists` | 409 (en la práctica no llegan al HTTP: el repo las devuelve ante el `UNIQUE` y los casos de uso las resuelven releyendo el existente) |
| Otros: fallas de infraestructura, el trigger de doble partida, una cuenta validada sin fila de saldo en `Apply` (inconsistencia de datos) | 500 |

---

## 5. Casos de uso

Directorio: [application/](../../context/ledger/application/)

| Caso de uso | Qué hace | Evento |
|---|---|---|
| `CreateAccount` | **Idempotente** por `(tenant, tipo, moneda)`: si ya existe devuelve su ID con `WasExisting=true`, sin evento. Si no existe, crea la cuenta y su saldo en 0 en una transacción. Ante una carrera, el `UNIQUE` → `ErrAccountAlreadyExists` → se relee la cuenta y se devuelve. | `ledger.account.created.v1` (solo si la crea) |
| `PostEntry` | Valida, chequea idempotencia (comparando los movimientos) y en **una transacción** guarda el asiento + postings, aplica los saldos y publica en el outbox. | `ledger.entry.posted.v1` |
| `ReverseEntry` | Carga el original, verifica el tenant y la idempotencia (`reverse_<clave original>`), y persiste la reversa igual que `PostEntry`. Ante una carrera devuelve la reversa que ganó. | `ledger.entry.reversed.v1` |
| `GetBalance` | Devuelve el saldo y la moneda de una cuenta **del tenant**. Una cuenta de otro tenant → `ErrAccountNotFound`. | — |

### 5.1 `PostEntry` en detalle

```
1. idempotency_key no vacía, ≥ 2 líneas, tenant_id válido
2. Parsear líneas (IDs y direcciones)
3. FindByIdempotencyKey(tenant, key)
     └─ existe → mismos movimientos (SameMovements) → {entry_id, was_existing: true}
                 otros movimientos → ErrIdempotencyKeyConflict (409)
4. validateAccounts: cada cuenta existe, es del tenant
   (si no → ErrAccountNotFound) y su moneda = la del posting (ErrAccountCurrencyMismatch)
   → NewJournalEntry (invariantes §4.3)
5. RunInTx {
     entryRepo.Save      → journal_entries + postings (con su position)
     balanceRepo.Apply   → UPDATE account_balances SET balance = balance ± amount
                           (en orden de account_id, §9)
     publisher.Publish   → outbox_messages
   }   // al COMMIT corre el trigger diferido check_entry_balance
   └─ UNIQUE (tenant, key) → ErrEntryAlreadyExists → se relee y sigue como en 3
```

- Si `occurred_at` viene vacío se usa el reloj. El HTTP siempre lo completa.
- "Mismos movimientos" compara cuentas, direcciones, montos y moneda, sin importar el orden. La descripción, la metadata y `occurred_at` no se comparan.

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

Binding HTTP: `idempotency_key` requerido, `lines` mínimo 2, y cada línea con `account_id`, `direction`, `amount ≥ 1` y `currency`. Un `account_id` que no es UUID → `400`. Una cuenta inexistente o de otro tenant → `404`. Una moneda distinta a la de la cuenta → `422`. La misma clave con otros movimientos → `409`.

### 6.3 Saldo

`GET /ledger/accounts/:accountID/balance` → `{"account_id":"…","balance":-12500,"currency":"ARS"}`. El saldo puede ser negativo: no hay control de sobregiro.

---

## 7. Eventos

### 7.1 Publicados (stream `LEDGER`)

| Subject | Payload | Consumidores |
|---|---|---|
| `ledger.account.created.v1` | `account_id`, `tenant_id`, `account_type`, `currency` | Audit |
| `ledger.entry.posted.v1` | `entry_id`, `tenant_id`, `idempotency_key`, `description`, `postings[]{account_id, direction, amount, currency}` | Audit, **Reporting** (proyección de saldos por comercio) |
| `ledger.entry.reversed.v1` | `reverse_entry_id`, `original_entry_id`, `tenant_id`, `postings[]` (los de la reversa) | Audit, Reporting |

Los headers son los estándar del Outbox (ver ONBOARDING.MD §7).

### 7.2 Consumidos

Todos corren en `cmd/worker` con `MaxDeliver: 5`. Un evento que el Ledger nunca va a aceptar se descarta sin reintentos (`eventbus.Permanent`): payload ilegible, tenant inválido, neto ≤ 0, `outcome` de disputa desconocido o un error de validación del dominio. Una cuenta inexistente sí se reintenta, porque puede crearla Onboarding. En los dos casos (descarte o reintentos agotados) el eventbus deja un log de error con el subject y la secuencia del stream.

| Durable | Filtro | Asiento (según el código) | Clave de idempotencia |
|---|---|---|---|
| `ledger-onboarding-consumer` | `onboarding.application.approved.v1` | Crea las 7 cuentas (§2.1) en `currency` (default `ARS`). La migración 000036 completó `in_transit` y `platform_fees` a los comercios aprobados antes. | — (no es un asiento) |
| `ledger-payment-consumer` | `payment.>` | **captured:** `in_transit` D (amount − psp_fee) · `merchant_balance` C (amount − platform_fee − psp_fee) · `platform_fees` C platform_fee (si es > 0; sin la cuenta, error). **refunded:** `merchant_balance` D · `in_transit` C, por `refund_amount` (las comisiones no se devuelven). El resto de los eventos se ignora. | `payment_captured_<payment_id>`, `payment_refunded_<payment_id>` |
| `ledger-payout-consumer` | `payout.>` | **initiated:** `merchant_balance` D · `payout_transit` C. **confirmed:** `payout_transit` D · `payout_sent` C. **failed:** `payout_transit` D · `merchant_balance` C | evento `idempotency_key` o `payout_initiated_<id>`; `payout_confirmed_<id>`; `payout_failed_<id>` |
| `ledger-dispute-consumer` | `dispute.>` | **opened:** `merchant_balance` D · `dispute_hold` C. **resolved won:** `dispute_hold` D · `merchant_balance` C. **resolved lost/accepted/expired:** `dispute_hold` D · `in_transit` C (el PSP descuenta el contracargo) | `dispute_opened_<id>`, `dispute_resolved_<id>` |

C = crédito (suma al saldo), D = débito (resta del saldo), según `Apply`.

### 7.3 Lecturas directas de otros BCs

[`payout/.../outbound/ledger/balance_checker.go`](../../context/payout/infrastructure/adapters/outbound/ledger/balance_checker.go) consulta `account_balances` ⨝ `ledger_accounts` (tipo `merchant_balance`) para validar el saldo antes de iniciar un payout: exige **disponible > 0 y ≥ monto**. Como el débito de `payout.initiated.v1` llega de forma asíncrona, el disponible resta los payouts cuyo asiento (`ledger_entry_key`) todavía no está en `journal_entries`, salvo los fallidos. Lee con la conexión de la tx de Payout, que tiene el tenant fijado y el advisory lock. Esto acopla Payout al esquema y a la **convención de signo** de Ledger.

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
                       uuid tenant_id FK
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
               char3 currency
               smallint position }
```

| Tabla | Restricciones clave | RLS |
|---|---|---|
| `ledger_accounts` | `UNIQUE (tenant_id, type, currency)`; `CHECK type IN (…7 tipos)` desde la migración 000007 | Sí (tenant + operador) |
| `account_balances` | PK = `account_id`; FK `(account_id, tenant_id)` → `ledger_accounts (id, tenant_id)`; `balance BIGINT` (puede ser negativo) | Sí (tenant + operador) |
| `journal_entries` | `UNIQUE (tenant_id, idempotency_key)`; índice `(tenant_id, created_at DESC)` | Sí (tenant + operador) |
| `postings` | `amount > 0`; `direction IN ('debit','credit')`; FK a la cuenta (sin chequear el tenant); `UNIQUE (entry_id, position)`, que conserva el orden de las líneas | Sí (tenant + operador) |

**Trigger `trg_check_entry_balance`**: `CONSTRAINT TRIGGER … AFTER INSERT ON postings DEFERRABLE INITIALLY DEFERRED`. Al hacer COMMIT, por cada posting insertado recalcula Σ débitos y Σ créditos del asiento, y aborta la transacción si hay ≥ 2 postings y no balancean. Es la segunda línea de defensa, por si alguien inserta por fuera del dominio.

**RLS** (migraciones 000037 y 000039): cada tabla tiene dos políticas, aislamiento por `NULLIF(current_setting('app.current_tenant', true), '')::uuid` y operador de plataforma (`app.platform_operator = 'on'`, lectura cross-tenant). Los repos fijan el tenant en cada operación con `withTenant` ([rls.go](../../context/ledger/infrastructure/adapters/outbound/postgres/rls.go)): el del argumento, o el del caller en las búsquedas por ID. Dentro de una transacción lo fija el `TxManager` con el tenant del contexto; los consumers lo toman del payload. Sin tenant, las consultas devuelven 0 filas en lugar de fallar. Hoy la app conecta como owner y el RLS no filtra (ONBOARDING.MD §10.2), pero ya funciona con un rol sin `BYPASSRLS`.

---

## 9. Concurrencia

- `UPDATE account_balances SET balance = balance + $delta` es atómico por fila: no se pierden actualizaciones.
- `Apply` actualiza los saldos en orden de `account_id`: dos asientos concurrentes sobre las mismas cuentas bloquean las filas en el mismo orden, así que el segundo espera al primero en lugar de generar un deadlock.
- Las lecturas de idempotencia y del asiento original (en la reversa) se hacen **fuera** de la transacción. Si otra ejecución con la misma clave gana el `UNIQUE` en el medio, el caso de uso relee y devuelve el asiento existente.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Severidad | Tema | Detalle |
|---|---|---|---|
| 1 | Producto | Alcance | Las cuentas de plataforma (`platform_fees`, `in_transit`) son por tenant: no hay revenue consolidado ni cuenta del banco del PSP. `reserve` no tiene movimientos. Un reembolso sale entero de `merchant_balance` (no se devuelven comisiones) y no hay reembolsos parciales. El Ledger no impide saldos negativos: un reembolso o una disputa pueden dejar `merchant_balance` en negativo (solo Payout valida el disponible antes de desembolsar). Sin listado de cuentas, asientos ni extracto. Sin endpoint de operador para ajustes manuales sobre un comercio (§6). |

---

## 11. Testing

| Capa | Archivos | Tipo |
|---|---|---|
| Dominio | `domain/*_test.go` | `Money`, invariantes de `JournalEntry`, `BuildReverse`, eventos. |
| Aplicación | `application/*_test.go` | `CreateAccount` (incluye idempotencia y carrera), `PostEntry` (idempotencia, clave reusada con otros movimientos, carrera contra el `UNIQUE`, cuenta inexistente, cuenta de otro tenant, moneda distinta a la de la cuenta), `GetBalance` (aislamiento), `ReverseEntry` (incluida la carrera). |
| HTTP | `inbound/http/*_test.go` | Handlers, router, `writeGuard` (403 en las escrituras; la lectura no se ve afectada), 404 cross-tenant y mapeo de errores. |
| NATS | `inbound/nats/*_test.go` | Los 4 consumers con repos y casos de uso falsos, más redelivery y reintento parcial del consumer de Onboarding. `convention_test.go` encadena pago, reembolso, disputas y payouts y verifica el saldo de cada cuenta según `Apply`, y que cada consumer ponga el tenant del payload en el contexto (RLS). `errors_test.go` distingue los errores permanentes de los que se reintentan. |
| Postgres | `outbound/postgres/*_test.go` | Integración contra PostgreSQL (`DATABASE_URL`), incluidos el mapeo de `23505` → `ErrAccountAlreadyExists`/`ErrEntryAlreadyExists` y el orden de los postings; se saltan si no hay DB. `lock_order_test.go` (sin DB) fija el orden de `Apply`. |

```bash
go test ./context/ledger/...
```

Falta un test de integración de punta a punta contra NATS y Postgres: aprobar onboarding → capturar pago → iniciar payout.

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
    ├── inbound/nats/  # onboarding, payment, payout, dispute consumers + errors (strict: errores permanentes)
    └── outbound/postgres/  # account_repo, entry_repo, balance_repo, rls (withTenant)
```

Cableado:

- [cmd/api/main.go](../../cmd/api/main.go): sección `Ledger` (rutas HTTP).
- [cmd/worker/main.go](../../cmd/worker/main.go): los 4 consumers.
