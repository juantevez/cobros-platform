# Payout — Documentación técnico-funcional

> **Bounded Context:** `context/payout`
> **Migraciones:** [`000007_payout_init`](../../migrations/000007_payout_init.up.sql) · [`000015_payout_idempotency`](../../migrations/000015_payout_idempotency.up.sql) · [`000048_payout_rls`](../../migrations/000048_payout_rls.up.sql) · [`000049_payout_bank_failure_detail`](../../migrations/000049_payout_bank_failure_detail.up.sql)
> **Stream NATS:** `PAYOUT` (`payout.>`)

---

## 1. Propósito y alcance

Payout orquesta el **desembolso** de fondos desde la plataforma hacia la cuenta bancaria del comercio: cierra el ciclo del dinero iniciado en Payment (captura → Ledger acredita `merchant_balance` → Payout transfiere al banco).

1. Obtiene la cuenta bancaria del comercio registrada en Onboarding.
2. Verifica el saldo disponible contra el Ledger, con un lock contable por `(tenant, moneda)` que serializa payouts concurrentes.
3. Registra el payout con una clave de idempotencia obligatoria y publica `payout.initiated.v1` (el Ledger reserva los fondos: `merchant_balance → payout_transit`).
4. Ejecuta la transferencia bancaria vía `BankTransferAdapter`.
5. Cierra el resultado: confirmado, rechazado o —si el banco no respondió— **indeterminado**, delegando la resolución a reconciliación.

### Estado actual de las integraciones

| Puerto | Implementación cableada | Comentario |
|---|---|---|
| `BankTransferAdapter` | `transfer/mock.Adapter` | Confirma siempre, salvo dos cuentas de prueba: `REJECT` (rechazo definitivo, envuelve `ErrBankRejected`) y `TIMEOUT` (resultado desconocido). El `IdempotencyKey` de la transferencia es `transfer_<payout_id>`; el mock lo ignora. **Nunca usar en producción.** |
| `BalanceChecker` / `BalanceLocker` | `ledger.BalanceChecker` | Query directa a `account_balances` (cuenta `merchant_balance` del tenant) menos los payouts aún no asentados en el Ledger, con la conexión de la tx + `pg_advisory_xact_lock` por `(tenant, currency)`. |
| `BankAccountProvider` | `onboarding.BankAccountProvider` | Query directa a `onboarding_bank_accounts` unida a solicitudes `approved`: solo cuentas `verified` (las verifica la aprobación del KYC) y en la moneda del payout; si no hay, `ErrNoBankAccount` (422). |
| `PayoutGate` | `tenant.PayoutGate` | Lee `tenants.status` y `tenant_compliance_holds` (contexto Auth): exige tenant `active` sin hold de cumplimiento vigente. |
| `EventPublisher` | `outbox.NewEventPublisher` | Publicación transaccional vía Outbox (ver LEDGER.MD §7). |
| `TxManager` | `pkg/postgres` | `RunInTx` propaga la tx por el contexto; `ConnFromContext` hace que repo y publisher participen de la misma transacción. |

### Fuera de alcance (hoy)

- Bancos reales (MP Transfers, Prex, adquirentes): solo existe el mock.
- Comisión de desembolso: no se descuenta ningún fee del monto transferido.
- Payouts parciales programados / retiros automáticos (scheduled payouts).
- Filtros por estado en `GET /payouts`; paginación real (solo `limit`).

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Payout / Desembolso** | Transferencia de fondos desde la plataforma a la cuenta bancaria del comercio. |
| **Clave de idempotencia** | La genera el caller (header `Idempotency-Key`). Obligatoria: un retry con la misma clave retorna el payout existente en lugar de desembolsar dos veces. |
| **Saldo disponible** | Saldo de la cuenta `merchant_balance` del tenant en el Ledger, para una moneda dada. |
| **`payout_transit`** | Cuenta del Ledger donde quedan "reservados" los fondos mientras la transferencia está en vuelo. |
| **`payout_sent`** | Cuenta del Ledger donde los fondos quedan una vez que el banco confirmó la acreditación. |
| **Resultado indeterminado (`unknown`)** | El banco no respondió (timeout/red). NO es un fallo: puede que los fondos hayan salido. Requiere resolución. |
| **`ledger_entry_key`** | Clave de idempotencia del asiento contable inicial (`payout_initiated_<id>`); viaja en `payout.initiated.v1`. |

---

## 3. Modelo de dominio

### 3.1 Agregado `Payout`

Archivo: [domain/payout.go](../../context/payout/domain/payout.go)

| Campo | Notas |
|---|---|
| `id`, `tenantID` | UUID. |
| `amount` | `Money` > 0, en unidades mínimas (centavos). Si el cmd trae `amount = 0`, se usa el saldo completo. |
| `bankAccount` | `BankAccountInfo` persistido como **snapshot** al momento del payout (auditoría). |
| `status` | Ver FSM (§3.2). |
| `bankReference` | Referencia del banco al confirmar. |
| `failureReason` | Motivo técnico; se persiste pero **no** se expone el detalle del banco en códigos HTTP distintos de 500. |
| `idempotencyKey` | Clave de negocio, única por `(tenant, key)`. |
| `ledgerEntryKey` | Idempotencia del asiento inicial en Ledger. |
| `initiatedAt` / `confirmedAt` / `failedAt` | `initiatedAt` se setea al pasar a `processing` (nombre histórico). |

`NewPayout` exige clave de idempotencia no vacía (`ErrIdempotencyKeyRequired`) y emite `PayoutInitiatedEvent`.

### 3.2 Máquina de estados

```mermaid
stateDiagram-v2
    [*] --> initiated: NewPayout
    initiated --> processing: MarkProcessing
    processing --> confirmed: Confirm
    processing --> failed: Fail (rechazo conocido)
    processing --> unknown: MarkUnknown (timeout / red)
    unknown --> confirmed: Confirm (resolución ✓)
    unknown --> failed: Fail (resolución ✗)
    confirmed --> [*]
    failed --> [*]
```

| Transición | Evento publicado | Efecto en Ledger |
|---|---|---|
| `initiated → processing` | — | — |
| creación (`initiated`) | `payout.initiated.v1` | `merchant_balance` DEBIT / `payout_transit` CREDIT |
| `processing → confirmed` | `payout.confirmed.v1` | `payout_transit` DEBIT / `payout_sent` CREDIT |
| `processing → failed` | `payout.failed.v1` | **Reversa**: `payout_transit` DEBIT / `merchant_balance` CREDIT |
| `processing → unknown` | `payout.unknown.v1` | **Ninguno**: los fondos permanecen en `payout_transit` hasta la resolución |
| `unknown → confirmed / failed` | `payout.confirmed.v1` / `payout.failed.v1` | Como en el camino normal (los asientos son idempotentes por clave) |

`IsFinal()` solo considera finales `confirmed` y `failed`: **`unknown` no es final** y exige intervención.

### 3.3 Value Objects

Archivo: [domain/vo.go](../../context/payout/domain/vo.go)

- `Money`: `NewMoney` exige monto > 0 e ISO-4217 de 3 letras (normalizada a mayúsculas).
- `PayoutStatus`: `initiated` | `processing` | `confirmed` | `failed` | `unknown`.
- `BankAccountInfo`: el número de cuenta es **campo privado**. Solo dos caminos de lectura:
  - `AccountNumber()` — restringido al camino outbound (adaptador bancario y persistencia del snapshot).
  - `MaskedAccountNumber()` — único permitido en respuestas HTTP y logs (`••••••••••••••••7370`).
  - `NewBankAccountInfo` valida (vacío o > 64 chars → `ErrNoBankAccount`); `MustBankAccountInfo` reconstituye desde datos ya persistidos sin revalidar.

### 3.4 Errores

Archivo: [domain/errors.go](../../context/payout/domain/errors.go)

| Error | HTTP |
|---|---|
| `ErrPayoutNotFound` | 404 |
| `ErrInsufficientBalance`, `ErrNoBankAccount`, `ErrInvalidTransition` | 422 |
| `ErrInvalidAmount`, `ErrInvalidCurrency`, `ErrIdempotencyKeyRequired`, `ErrInvalidID` (payout o tenant que no es UUID) | 400 |
| `ErrPayoutsNotAllowed` (tenant no activo, suspendido o en hold de cumplimiento) | 403, mensaje genérico `payouts are not available for this account` |
| `ErrIdempotencyKeyConflict` (misma clave, otro monto/moneda), `ErrPayoutAlreadyExists` (carrera perdida contra el UNIQUE) | 409 |
| `ErrPayoutRejected` (el banco rechazó: su error envuelve `ErrBankRejected`) | **422** con `payout_id` y `status: failed`; el detalle del banco no se expone |
| `ErrBankTransferUnknown` (cualquier otro error de la transferencia) | **202** con `payout_id` y `status: unknown`: el dinero sigue reservado y el recuperador lo resuelve (§5.3) |
| Errores de infraestructura | 500 (sin filtrar detalle interno) |

---

## 4. Flujo funcional

```mermaid
sequenceDiagram
    autonumber
    actor C as Comercio (JWT)
    participant P as Payout API
    participant O as Onboarding (PG)
    participant L as Ledger (PG)
    participant DB as PostgreSQL
    participant B as Banco (mock)
    participant N as NATS

    C->>P: POST /payouts {amount?, currency?} + Idempotency-Key
    P->>O: GetBankAccount(tenant)  ← solicitud approved
    alt sin cuenta
        P-->>C: 422 ErrNoBankAccount
    end
    P->>DB: BEGIN tx
    P->>L: pg_advisory_xact_lock(tenant, currency)   ← serializa concurrentes
    P->>DB: FindByIdempotencyKey(tenant, key)
    alt existe y coincide
        P->>DB: COMMIT
        P-->>C: 200 payout existente (replay, sin segunda transferencia)
    else existe con otro monto/moneda
        P->>DB: ROLLBACK
        P-->>C: 409 conflict
    end
    P->>L: SELECT merchant_balance (dentro del lock)
    P->>DB: INSERT payout (initiated) + outbox payout.initiated.v1
    P->>DB: COMMIT
    N-->>N: Ledger consumer reserva merchant_balance → payout_transit
    P->>DB: UPDATE processing
    P->>B: Transfer(importe, cuenta, idem key)
    alt banco confirma
        P->>DB: tx{UPDATE confirmed + outbox payout.confirmed.v1}
        P-->>C: 201
        N-->>N: Ledger cierra payout_transit → payout_sent
    else rechazo explícito
        P->>DB: tx{UPDATE failed + outbox payout.failed.v1}
        P-->>C: 500
        N-->>N: Ledger revierte payout_transit → merchant_balance
    else timeout / error de red
        P->>DB: tx{UPDATE unknown + outbox payout.unknown.v1}
        P-->>C: 500 (awaiting reconciliation)
        Note over N: SIN reversa: fondos siguen en payout_transit
    end
```

---

## 5. Casos de uso

Directorio: [application/](../../context/payout/application/)

### 5.1 `InitiatePayoutUseCase`

Archivo: [initiate_payout.go](../../context/payout/application/initiate_payout.go)

| Paso | Acción | ¿En tx? | Si falla |
|---|---|---|---|
| 1 | Parsear tenant; exigir `IdempotencyKey` no vacía | — | 400, sin persistir |
| 1b | `gate.EnsurePayoutsAllowed`: tenant `active` y sin hold de cumplimiento. Va antes del replay: un hold también bloquea reintentos. Moneda default `ARS` | — | 403 `ErrPayoutsNotAllowed`, sin persistir |
| 2 | `bankProvider.GetBankAccount` (cuenta de solicitud `approved`) | — | 422/500, sin persistir |
| 3a | `BalanceLocker.LockBalance` — `pg_advisory_xact_lock` keyed por `(tenant, currency)` | Sí | aborta la tx |
| 3b | Replay: `FindByIdempotencyKey`; si existe y `MatchesRequest` → devolverlo (`Replayed: true`); si existe con otro monto/moneda → `ErrIdempotencyKeyConflict` | Sí | 409 |
| 3c | `GetAvailableBalance` **con el lock tomado**; monto efectivo = `cmd.Amount` o saldo completo si es 0; exige `0 < monto ≤ disponible` | Sí | 422 `ErrInsufficientBalance` |
| 3d | `NewPayout` + `repo.Save` + publish `payout.initiated.v1` | Sí (misma tx) | UNIQUE violado → `ErrPayoutAlreadyExists` (409; el retry encontrará el registro) |
| 4 | `MarkProcessing` + `repo.Update` | No | 500; payout queda `initiated` con fondos reservados |
| 5 | `transferAdapter.Transfer` (`IdempotencyKey: transfer_<payout_id>`) | — | ver abajo |
| 6a | OK → `Confirm(ref)` + tx{Update condicional a `processing` + publish `payout.confirmed.v1`} | Sí | Si el recuperador ya lo resolvió (`ErrPayoutChanged`), se devuelve el estado que quedó. Otro error: 500 y queda `processing`, y lo resuelve el recuperador (§5.3) |
| 6b | Rechazo explícito del banco (el error envuelve `ErrBankRejected`) → `Fail(reason)` + tx{Update condicional a `processing` + publish `payout.failed.v1`} | Sí | Error al persistir: se devuelve (antes se descartaba) y queda `processing` para el recuperador |
| 6c | **Cualquier otro error** (timeout, red, 5xx, error imprevisto del SDK) → `MarkUnknown` + tx{Update condicional a `processing` + publish `payout.unknown.v1`}, **sin reversa** | Sí | Ídem: el error se devuelve y el recuperador resuelve el payout |

Solo un error tipado del adaptador (`ErrBankRejected`, [transfer_errors.go](../../context/payout/application/transfer_errors.go)) revierte fondos; cualquier otra cosa es *no sabemos*. Antes era al revés: se reconocía el timeout por tipo o mensaje y todo lo demás se trataba como rechazo, así que un 5xx o un error imprevisto revertían un desembolso que el banco pudo haber pagado.

Resultado: `{payout_id, amount, currency, status, bank_reference, replayed}` (snake_case, como `PayoutView`).

### 5.3 Recuperador de payouts (`PayoutRecoverer`)

Archivo: [recover_payouts.go](../../context/payout/application/recover_payouts.go). Corre en `cmd/worker`: un ciclo al arrancar y después cada minuto.

Un payout queda en `unknown` cuando el banco no confirmó el resultado, y en `processing` cuando el proceso cayó o falló la persistencia. El recuperador toma hasta 50 payouts en `processing` o `unknown` sin cambios hace más de **2 minutos** y le pregunta al banco por la clave de la transferencia (`transfer_<payout_id>`, `GetTransfer`):

| El banco responde | Acción |
|---|---|
| Acreditada | `Confirm` → `payout.confirmed.v1` (el Ledger cierra el asiento) |
| Rechazada | `Fail` → `payout.failed.v1` (el Ledger revierte: el saldo vuelve al comercio) |
| Sin registro, hace menos de 30 min | Nada: el banco todavía puede reflejarla |
| Sin registro, hace 30 min o más | `Fail` (el banco nunca la recibió) → `payout.failed.v1` |
| Error | Nada: se reintenta en el próximo ciclo |

La escritura es condicional al estado que se leyó y corre con el tenant del payout: si el request original lo resolvió mientras tanto, el recuperador no escribe ni publica. El banco `mock` no guarda estado y responde siempre *sin registro* (su único resultado desconocido, la cuenta `TIMEOUT`, simula un timeout antes de llegar al banco).

### 5.2 Consultas

Archivo: [query_payouts.go](../../context/payout/application/query_payouts.go)

- `GetPayoutUseCase`: busca por ID; si es de otro tenant → `ErrPayoutNotFound` (no revela existencia).
- `ListPayoutsUseCase`: `ORDER BY created_at DESC LIMIT n` (default 20).
- Ambos devuelven `PayoutView` con la cuenta **enmascarada** (`MaskedAccountNumber`).

---

## 6. API HTTP

Archivo: [payout_handler.go](../../context/payout/infrastructure/adapters/inbound/http/payout_handler.go)

Las rutas `/api/v1/payouts` cuelgan del grupo `protected` de `cmd/api` con `JWTMiddleware` (solo JWT) y un guard por ruta (`RouteGuards`; `RegisterRoutes` entra en pánico si falta alguno):

| Guard | Rutas | En producción |
|---|---|---|
| `Create` | `POST /payouts` | `RequirePermission(payouts:create)`: solo `admin`, y sin scopes, así que una API key no puede desembolsar |
| `Read` | `GET /payouts`, `GET /payouts/:id` | `RequirePermission(payments:read)` |

Sin permiso → 403. El tenant sale del token.

| Método | Ruta | Descripción | Éxito |
|---|---|---|---|
| `POST` | `/payouts` | Solicita un desembolso | `201` nuevo / `200` replay idempotente |
| `GET` | `/payouts?limit=20` | Lista payouts del tenant (`limit` por defecto 20, máximo 100; uno mayor se recorta) | `200` |
| `GET` | `/payouts/:payoutID` | Consulta un payout | `200` |

### 6.1 Desembolsar — `POST /payouts`

Header **`Idempotency-Key: payout-2026-10-27-01`** (obligatorio; alternativa: campo `idempotency_key` en el body). Body opcional:

```json
{ "amount": 0, "currency": "ARS" }
```

`amount = 0` (u omitido) → desembolsa el **saldo disponible completo**.

```json
// 201
{ "payout_id": "…", "amount": 125000, "currency": "ARS", "status": "confirmed", "bank_reference": "MOCK-BANK-1a2b3c4d", "replayed": false }
```

| Caso | HTTP |
|---|---|
| Sin clave de idempotencia | 400 |
| Moneda inválida / monto ≤ 0 tras resolver el default | 400 |
| Tenant sin cuenta bancaria aprobada | 422 |
| Saldo insuficiente | 422 |
| Retry con la misma clave, mismo monto/moneda | **200** con el payout existente (no se transfiere dos veces) |
| Misma clave con otro monto/moneda | 409 |
| Carrera contra el UNIQUE (otra tx commiteó primero) | 409 — el cliente debe reintentar |
| Rechazo del banco (cuenta `REJECT` en el mock) | **422** `{"error":"payout rejected by the bank","payout_id":"…","status":"failed"}` |
| El banco no confirmó (timeout, red, 5xx; cuenta `TIMEOUT` en el mock) | **202** `{"payout_id":"…","status":"unknown","detail":"…"}`; lo resuelve el recuperador (§5.3) |

### 6.2 Consultar — `GET /payouts/:payoutID`

```json
{
  "id": "…", "tenant_id": "…", "amount": 125000, "currency": "ARS",
  "status": "confirmed",
  "bank_account_type": "CBU", "bank_account_number": "••••••••••••••••7370",
  "holder_name": "Comercio SA",
  "bank_reference": "MOCK-BANK-1a2b3c4d",
  "initiated_at": "2026-10-27T14:00:00Z", "confirmed_at": "2026-10-27T14:00:01Z",
  "created_at": "2026-10-27T14:00:00Z"
}
```

`failure_reason` es un motivo público: `rejected by the bank`, `the bank did not confirm the transfer` (unknown) o `the bank has no record of the transfer` (recuperador). El error crudo del banco, que puede traer datos de la cuenta, se guarda en `bank_failure_detail` y no se expone en la API, en los eventos ni, por ende, en el email ni el webhook.

---

## 7. Eventos y consumidores

Archivo: [domain/events.go](../../context/payout/domain/events.go). Se publican por Outbox en el stream `PAYOUT` (`payout.>`), subject por evento.

| Subject | Payload |
|---|---|
| `payout.initiated.v1` | `payout_id`, `tenant_id`, `amount`, `currency`, `idempotency_key` (= `ledger_entry_key`) |
| `payout.confirmed.v1` | `payout_id`, `tenant_id`, `amount`, `currency`, `bank_reference` |
| `payout.failed.v1` | `payout_id`, `tenant_id`, `amount`, `currency`, `failure_reason` |
| `payout.unknown.v1` | `payout_id`, `tenant_id`, `amount`, `currency`, `failure_reason` (`the bank did not confirm the transfer`) |

### 7.1 Consumidores

| Consumidor | Filtro | Acción |
|---|---|---|
| **Ledger** ([payout_consumer.go](../../context/ledger/infrastructure/adapters/inbound/nats/payout_consumer.go)) | `payout.>` | Asientos de initiated / confirmed / reversa de failed (ver tabla FSM §3.2). Ignora `payout.unknown.v1` — correcto: no hay reversa. Cada asiento es idempotente por clave (`payout_initiated_…`, `payout_confirmed_…`, `payout_failed_…`). |
| **Notification** | `payout.>` | Emails en `payout.confirmed.v1`, `payout.failed.v1` y `payout.unknown.v1` (este último avisa que el desembolso está en verificación y los fondos siguen reservados, sin el motivo técnico). |
| **Webhook** | `payout.>` | Entrega a endpoints suscriptos a `payout.confirmed` / `payout.failed`. |
| Compliance / Reporting | — | **No consumen eventos de payout** hoy. |

Consistencia eventual: el payout **no espera** al Ledger; la reserva de fondos puede materializarse unos instantes después del commit (ver LEDGER.MD §7 por outbox).

---

## 8. Persistencia

Tabla `payouts` ([000007](../../migrations/000007_payout_init.up.sql) + [000015](../../migrations/000015_payout_idempotency.up.sql)):

| Grupo | Columnas |
|---|---|
| Identidad | `id` PK, `tenant_id`, `idempotency_key` (agregada en 000015) |
| Monto | `amount BIGINT CHECK (> 0)`, `currency CHAR(3)` |
| Cuenta destino (snapshot) | `bank_acct_type`, `bank_acct_num` (en claro, ver §9), `bank_name`, `holder_name` |
| Estado | `status` CHECK `('initiated','processing','confirmed','failed','unknown')`, `bank_reference`, `failure_reason` (público), `ledger_entry_key` |
| Diagnóstico | `bank_failure_detail` (000049): error crudo del banco al rechazar o no confirmar; interno |
| Tiempos | `initiated_at`, `confirmed_at`, `failed_at`, `created_at`, `updated_at` |

- `UNIQUE (tenant_id, idempotency_key) WHERE idempotency_key <> ''` (índice parcial; permite legados sin clave).
- Índice `(tenant_id, status, created_at DESC)`.
- RLS (migración 000048): aislamiento por `NULLIF(current_setting('app.current_tenant', true), '')::uuid` y política de operador de plataforma de **solo lectura** (la usa el recuperador para listar los payouts sin resolver).
- Cada método del repo corre en la sesión RLS ([rls.go](../../context/payout/infrastructure/adapters/outbound/postgres/rls.go)): con el tenant del payout al escribir y el del llamador en `FindByID`. `ListUnresolved` corre como operador y el recuperador escribe cada payout con su tenant. `BankAccountProvider` lee las tablas de Onboarding (con RLS) en la sesión del tenant del payout. El aislamiento se verifica con un rol sin `BYPASSRLS` (§11).
- `Update` reescribe solo estado, referencia, motivo y fechas, y es **condicional al estado de partida** (`WHERE id=$1 AND status=<from>`): si otra ejecución ya movió el payout, no escribe y devuelve `ErrPayoutChanged`. Monto y snapshot bancario son inmutables.
- `ListUnresolved` (recuperador) lista los payouts en `processing` o `unknown` con `updated_at` anterior al corte, de todos los tenants.
- Migraciones de ledger: `000007` amplía el CHECK de `ledger_accounts.type` con `payout_transit` y `payout_sent` (las crea el consumer de Onboarding).

---

## 9. Seguridad y cumplimiento

- **Datos bancarios:** el número de cuenta vive encapsulado en `BankAccountInfo`; la API de lectura solo expone `MaskedAccountNumber()`. El snapshot en `payouts.bank_acct_num` queda **en claro** (necesario para reproducir la transferencia y auditar); no hay cifrado en reposo (§10).
- **Autorización:** desembolsar requiere `payouts:create` (solo `admin`); consultar, `payments:read`. Las API keys no pueden desembolsar.
- **Aislamiento:** `Get` verifica el tenant del payout; `List` y `Initiate` toman el tenant del token.
- **Estado del comercio y AML:** `PayoutGate` bloquea desembolsos de tenants no activos, suspendidos o en hold preventivo de cumplimiento (ver [COMPLIANCE.md](COMPLIANCE.md) §7.3). El mensaje no revela el motivo (anti tipping-off). Un payout iniciado antes del hold sigue su curso.
- **Reserva contable:** el lock advisory por `(tenant, currency)` dentro de la misma tx que lee el saldo e inserta el payout serializa los requests concurrentes (§10). Como el Ledger debita `merchant_balance` de forma asíncrona, el disponible descuenta los payouts cuyo asiento de `initiated` todavía no existe (salvo los fallidos): sin eso, dos payouts seguidos antes de que corriera el consumer leían el mismo saldo.
- **Idempotencia de extremo a extremo:** clave de negocio (HTTP) → UNIQUE (PG) → `transfer_<payout_id>` (banco) → `ledger_entry_key` (asiento). Cuatro capas: retries del caller nunca duplican dinero.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Severidad | Tema | Detalle |
|---|---|---|---|
| 1 | Producto | Alcance | `bank_acct_num` (CBU) en claro, sin cifrado ni retención. Sin comisión de desembolso. Una sola cuenta por tenant. Desembolsos por API key (hoy solo JWT). El mock de transferencia ignora la clave de idempotencia (un banco real debe honrarla). Los adaptadores reales tienen que envolver `ErrBankRejected` en sus rechazos definitivos. |

---

## 11. Testing

| Capa | Archivos | Cobertura relevante |
|---|---|---|
| Dominio | `domain/payout_test.go`, `vo_test.go`, `events_test.go` | FSM completa incluida `unknown → confirmed/failed`, máscara de cuenta, claves de idempotencia, payloads de eventos. |
| Aplicación | `application/initiate_payout_test.go` (+ `mocks_test.go`, `query_payouts_test.go`) | 26 subtests: happy path, saldo completo, replay idempotente, conflicto de clave, rejection→failed, timeout/network→unknown (sin reversa), fallos de cada paso.. Recuperador: confirmado, rechazado, sin registro antes y después de 30 min, error de consulta, resuelto mientras tanto; transiciones condicionales; errores de persistencia que se devuelven. |
| HTTP | `inbound/http/payout_handler_test.go` | Mapeo de errores (400/404/409/422/500), rechazo del banco → 422 y resultado desconocido → 202 (con `payout_id`, sin el detalle del banco), header `Idempotency-Key`, 200 vs 201, respuesta enmascarada, guard por ruta (403; pánico si falta alguno). |
| Adapters | `outbound/ledger/balance_checker_test.go`, `outbound/onboarding/bank_account_provider_test.go` e `integration_test.go`, `outbound/transfer/mock/adapter_test.go` | Query de saldo, lock requiere tx activa, cuenta aprobada, verificada y en la moneda del payout (integración contra solicitudes reales de Onboarding: otra moneda o sin verificar → `ErrNoBankAccount`), mock confirm/reject/timeout. |
| Postgres | `outbound/postgres/payout_repo_test.go`, `integration_test.go`, `rls_test.go` | pgxmock: INSERT de 18 columnas con NULLs, mapeo de UNIQUE → `ErrPayoutAlreadyExists`, `Update` condicional (0 filas → `ErrPayoutChanged`), `ListUnresolved`, scans. Integración contra PostgreSQL (`DATABASE_URL`; se saltan sin DB): confirmar y fallar el mismo payout a la vez (gana uno) y `ListUnresolved`. RLS con un rol sin `BYPASSRLS` (`payout_rls_probe`): aislamiento entre tenants, operador que lista pero no escribe, desembolso a la cuenta aprobada en Onboarding y recuperador cerrando un `unknown`. |
| Ledger (cross-BC) | `context/ledger/.../payout_consumer_test.go` | Asientos de initiated/confirmed/reversa de failed. |

```bash
go test ./context/payout/...
```

Faltan: test de concurrencia real del advisory lock (#4, requiere Postgres) y pruebas de resolución de `unknown` cuando exista el resolver (#3).

---

## 12. Mapa de código

```
context/payout/
├── domain/
│   ├── payout.go          # Agregado Payout + FSM (incl. unknown)
│   ├── vo.go              # IDs, PayoutStatus, Money, BankAccountInfo (encapsulado)
│   ├── events.go          # initiated / confirmed / failed / unknown
│   └── errors.go
├── application/
│   ├── ports.go           # PayoutRepository, PayoutGate, BalanceChecker, BalanceLocker,
│   │                      # BankAccountProvider, BankTransferAdapter, TxManager, EventPublisher
│   ├── commands.go        # InitiatePayoutCmd/Result, PayoutView
│   ├── initiate_payout.go # Caso de uso principal (lock + idempotencia + unknown)
│   ├── query_payouts.go   # GetPayout, ListPayouts
│   ├── recover_payouts.go # PayoutRecoverer (worker): resuelve payouts unknown o trabados
│   └── transfer_errors.go # ErrBankRejected: solo el rechazo explícito revierte fondos
└── infrastructure/adapters/
    ├── inbound/http/payout_handler.go
    └── outbound/
        ├── postgres/payout_repo.go, rls.go   # cada método en la sesión RLS
        ├── transfer/mock/adapter.go
        ├── ledger/balance_checker.go        # saldo + pg_advisory_xact_lock
        ├── tenant/gate.go                   # PayoutGate: tenant activo y sin hold AML
        └── onboarding/bank_account_provider.go
```

Consumidor contable: [context/ledger/.../nats/payout_consumer.go](../../context/ledger/infrastructure/adapters/inbound/nats/payout_consumer.go).
Cableado: [cmd/api/main.go](../../cmd/api/main.go) sección `Payouts`; consumers y el recuperador en [cmd/worker/main.go](../../cmd/worker/main.go).
