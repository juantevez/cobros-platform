# Reconciliation — Documentación técnico-funcional

> **Bounded Context:** `context/reconciliation`
> **Migración:** [`000010_reconciliation_init`](../../migrations/000010_reconciliation_init.up.sql)
> **Stream NATS publicado:** `RECONCILIATION` (`reconciliation.>`) vía outbox — no consume eventos
> **Lee tablas de otros BCs:** `payments` (Payment), `journal_entries` + `postings` (Ledger)

---

## 1. Propósito

Reconciliation es el contexto que **verifica que lo registrado por la plataforma coincide
con la realidad**, y deja cada diferencia como una **discrepancia** para que un operador la
revise. No corrige nada: detecta y registra.

Hay dos tipos de conciliación (un *run* por período y tipo):

| Tipo | Compara | Origen externo |
|---|---|---|
| `payment` | Pagos del sistema (`payments`) contra el informe del PSP | CSV que sube el operador |
| `internal_ledger` | `SUM(créditos) == SUM(débitos)` de los asientos del período | — (chequeo interno) |

Responsabilidades:

1. Crear runs de conciliación para un período `[period_from, period_to)`.
2. Comparar pago por pago contra el CSV del PSP (matching por `psp_reference`).
3. Verificar el balance de doble partida del Ledger en el período.
4. Persistir las discrepancias y exponer el reporte del run.
5. Permitir al operador **resolver** o **ignorar** cada discrepancia con notas.
6. Publicar `reconciliation.run.completed.v1` al terminar un run (lo consume Audit).

### Fuera de alcance (hoy)

- Ingesta automática del informe del PSP (API o SFTP) y ejecución programada: todo es manual vía HTTP.
- Conciliación de **payouts** contra el banco: Payout deja los payouts `unknown` "a la espera de
  conciliación", pero no existe un tipo de run que los resuelva.
- Corrección de los datos: resolver una discrepancia solo cambia su estado.
- Runs **globales** (sin tenant) desde la API: el dominio los soporta, pero el handler exige tenant.

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Run** (`ReconciliationRun`) | Una ejecución de conciliación de un tipo sobre un período. Tiene estado y contadores. |
| **Período** | Intervalo semiabierto `[period_from, period_to)`, con `period_from < period_to`. |
| **Run global** | Run sin `tenant_id`: abarca todos los tenants. Solo para procesos internos. |
| **Informe del PSP** | CSV con las transacciones que el PSP dice haber procesado. |
| **`psp_reference`** | Referencia del pago en el PSP. Es la clave de matching (`transaction_id` en el CSV). |
| **Discrepancia** | Diferencia detectada en un run. Queda `open` hasta que un operador la resuelve o la ignora. |
| **Match** | Pago presente en ambos lados con el mismo monto y sin un desacuerdo crítico de estado. |
| **Imbalance** | Créditos menos débitos del Ledger en el período, en centavos. `0` = balanceado. |

---

## 3. Flujo funcional

El operador crea el run y después lo procesa en un segundo request (subiendo el CSV o pidiendo
el chequeo interno). El procesamiento es **síncrono**: cuando el request responde, el run ya
terminó.

```mermaid
sequenceDiagram
    autonumber
    actor O as Operador
    participant H as Handler HTTP
    participant UC as ProcessReportUseCase
    participant RR as RunRepo
    participant CSV as CSVReportParser
    participant PR as PaymentReader
    participant DR as DiscrepancyRepo
    participant OB as Outbox

    O->>H: POST /reconciliation/runs (type=payment, período)
    H->>RR: Save(run pending)
    H-->>O: 201 {run_id}
    O->>H: POST /runs/:runID/report (CSV)
    H->>UC: Execute(tenant, runID, CSV)
    UC->>RR: FindByID + ownedBy(tenant)
    UC->>UC: run.Start() (solo desde pending)
    UC->>RR: Update(running)
    UC->>CSV: Parse(CSV)
    UC->>PR: ReadByPeriod(tenant, from, to)
    UC->>UC: indexar ambos lados por psp_reference y comparar
    alt error al parsear o leer
        UC->>RR: Update(failed, error_msg)
        UC-->>H: error (400 formato inválido, 500 resto)
    end
    UC->>DR: SaveAll(discrepancias)
    UC->>UC: run.Complete(total, matched, discrepancias)
    UC->>RR: Update(completed)
    UC->>OB: Publish(reconciliation.run.completed.v1)
    H-->>O: 202
    O->>H: GET /runs/:runID?status=open
    H-->>O: 200 {run, discrepancies}
    O->>H: POST /discrepancies/:id/resolve (resolve o ignore)
    H-->>O: 204
```

`internal_ledger` sigue el mismo esqueleto con `POST /runs/:runID/process-internal`: en lugar de
CSV y `PaymentReader`, el `LedgerChecker` calcula el imbalance. Si no es cero se crea **una**
discrepancia `ledger_imbalance`.

### Reglas de matching (`payment`)

Ambos lados se indexan por referencia. Los pagos del sistema sin `psp_reference` (no llegaron al
PSP) se descartan del índice.

| Situación | Resultado |
|---|---|
| En el sistema, no en el PSP | `missing_in_psp` (`system_value` = JSON del pago) |
| En el PSP, no en el sistema | `missing_in_system` (`external_value` = JSON del registro) |
| En ambos, monto distinto | `amount_mismatch` (`"<monto> <moneda>"` de cada lado) |
| En ambos, estado crítico distinto | `status_mismatch` |
| Resto | match |

Solo tres pares de estado (sistema → PSP) cuentan como críticos: `captured`→`rejected`,
`captured`→`failed` y `failed`→`captured`. Cualquier otra combinación es match (por ejemplo,
`captured` en el sistema y `refunded` en el PSP: el reembolso puede no haberse procesado aún).

Contadores: `total_records = max(registros del CSV, pagos del sistema)`, `matched_count` = pagos
del sistema que matchean, `discrepancy_count` = discrepancias creadas.

---

## 4. Modelo de dominio

### 4.1 Agregado `ReconciliationRun`

| Campo | Notas |
|---|---|
| `id`, `tenant_id` | `tenant_id` vacío = run global. |
| `type` | `payment` \| `internal_ledger`. |
| `status` | Ver la máquina de estados. |
| `period_from`, `period_to` | Validados en el constructor (`ErrInvalidPeriod`). |
| `total_records`, `matched_count`, `discrepancy_count` | Se fijan en `Complete`. |
| `error_msg` | Se fija en `Fail`. |
| `created_at`, `completed_at` | `completed_at` se fija en `Complete` y en `Fail`. |

```mermaid
stateDiagram-v2
    [*] --> pending: NewReconciliationRun
    pending --> running: Start()
    running --> completed: Complete()
    running --> failed: Fail(reason)
    completed --> [*]
    failed --> [*]
```

- `Start()` solo desde `pending`; en cualquier otro estado devuelve `ErrRunAlreadyRunning`. Un
  run `failed` no se reintenta: hay que crear otro.
- `Complete()` registra `ReconciliationCompletedEvent`. `Fail()` no emite evento.

### 4.2 Entidad `Discrepancy`

| Campo | Notas |
|---|---|
| `run_id`, `tenant_id` | Tenant copiado del run. |
| `type` | `missing_in_psp`, `missing_in_system`, `amount_mismatch`, `status_mismatch`, `ledger_imbalance`. |
| `record_id` | `psp_reference` del pago, o `"global"` para `ledger_imbalance`. |
| `system_value`, `external_value` | Texto libre: JSON o `"<monto> <moneda>"`. |
| `status` | `open` → `resolved` \| `ignored`. |
| `resolved_by`, `notes`, `resolved_at` | Se fijan al resolver o ignorar. |

`Resolve` e `Ignore` solo desde `open`; si no, `ErrDiscrepancyAlreadyResolved`.

### 4.3 Value Objects y errores

| VO | Validación |
|---|---|
| `RunID`, `DiscrepancyID`, `TenantID` | UUID (`Parse*`). El error **no** es un sentinel del dominio. |
| `ReconciliationType` | `ParseReconciliationType` (error no tipado). |
| `RunStatus`, `DiscrepancyStatus`, `DiscrepancyType` | Constantes; sin `Parse` (se reconstituyen desde la base). |

| Error | HTTP |
|---|---|
| `ErrRunNotFound`, `ErrDiscrepancyNotFound` | 404 (también cuando el recurso es de otro tenant) |
| `ErrRunAlreadyRunning`, `ErrDiscrepancyAlreadyResolved` | 409 |
| `ErrInvalidPeriod`, `ErrEmptyReport`, `ErrInvalidReportFormat` | 400 |
| `ErrRunNotCompleted` | Declarado, sin uso |
| Cualquier otro (incluidos los `Parse*`) | 500 |

---

## 5. Casos de uso

| Caso de uso | Qué hace |
|---|---|
| `StartReconciliationUseCase` | Valida tipo, período y tenant, y guarda el run en `pending`. |
| `ProcessReportUseCase` | `Start` → parsea el CSV → lee `payments` del período → compara → guarda discrepancias → `Complete` → publica el evento. |
| `ProcessInternalUseCase` | `Start` → `CheckBalance` → 0 o 1 discrepancia → `Complete` (`total=1`, `matched` = 1 si balancea) → publica el evento. |
| `ResolveDiscrepancyUseCase` | Busca la discrepancia, verifica el tenant, aplica `resolve` o `ignore` con `resolved_by` y notas. |
| `GetReportUseCase` | Run + discrepancias, con filtro opcional por estado (`limit` por defecto 100). |
| `ListRunsUseCase` | Runs del tenant, `created_at DESC` (`limit` por defecto 20). |

**Aislamiento en la aplicación:** `ownedBy` compara el `tenant_id` del run con el del caller. Un
tenant nunca ve runs de otro ni globales, y un caller sin tenant solo ve globales. El mismo
criterio aplica a las discrepancias. Si el recurso no pertenece al caller, la respuesta es
"no encontrado".

**Fallos:** si el parseo, la lectura de pagos, el chequeo del Ledger o el guardado de
discrepancias fallan, el run pasa a `failed` con el mensaje del error. Los pasos **no** comparten
transacción (ver §10).

---

## 6. API HTTP

Grupo `protected` (JWT) → `/api/v1/reconciliation`. El tenant sale del token; sin tenant en el
contexto → **401**. No hay guard de rol.

| Método | Ruta | Descripción | Respuestas |
|---|---|---|---|
| `POST` | `/runs` | Body `{"type","period_from","period_to"}` (RFC3339) → `{run_id}` | 201 · 400 · 401 · 500 (tipo inválido) |
| `GET` | `/runs?limit=20` | `{runs:[…], count}` | 200 · 401 · 500 |
| `GET` | `/runs/:runID?status=open&limit=100` | `{run, discrepancies:[…]}` | 200 · 401 · 404 · 500 |
| `POST` | `/runs/:runID/report` | CSV del PSP en el body (máx. 50 MB) | 202 · 400 · 401 · 404 · 409 · 500 |
| `POST` | `/runs/:runID/process-internal` | Chequeo de balance del Ledger | 202 · 401 · 404 · 409 · 500 |
| `POST` | `/discrepancies/:discrepancyID/resolve` | Body `{"action":"resolve"\|"ignore","resolved_by","notes"}` | 204 · 400 · 401 · 404 · 409 · 500 |

### Formato del CSV

```
transaction_id,amount,currency,status,processed_at
PSP-REF-001,10000,ARS,captured,2026-06-01T12:00:00Z
PSP-REF-002,5000,ARS,rejected,2026-06-01T12:05:00Z
```

- Encabezados obligatorios: `transaction_id`, `amount`, `currency`, `status`. `processed_at`
  (RFC3339) es opcional. No importan mayúsculas ni orden, y las columnas extra se ignoran.
- `amount` en centavos (entero). `currency` se normaliza a mayúsculas, `status` a minúsculas.
- Una fila mal formada (comillas sin cerrar, cantidad de columnas distinta) invalida el informe
  completo con `ErrInvalidReportFormat`.

---

## 7. Eventos

### 7.1 Publicados

| Subject | Cuándo | Payload |
|---|---|---|
| `reconciliation.run.completed.v1` | Al completar un run (con o sin discrepancias) | `run_id`, `tenant_id`, `type`, `total_records`, `matched_count`, `discrepancy_count` |

Se publica por el outbox (`pkg/outbox`). El único consumidor es **Audit**
(`audit-reconciliation-consumer`, recurso `reconciliation_run`). Notification no lo notifica.

No hay eventos para runs fallidos ni para la resolución de discrepancias.

### 7.2 Consumidos

Ninguno.

---

## 8. Persistencia

Migración `000010_reconciliation_init`:

| Tabla | Claves e índices | Checks |
|---|---|---|
| `reconciliation_runs` | PK `id`; `idx_recon_runs_tenant (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL`; `idx_recon_runs_created (created_at DESC)` | `type`, `status`, `period_from < period_to` |
| `reconciliation_discrepancies` | PK `id`; FK `run_id` → runs; `idx_recon_disc_run (run_id, status)`; `idx_recon_disc_tenant` | `type` (5 valores), `status` |

`tenant_id` es nullable en ambas tablas (runs globales). **Ninguna de las dos tiene RLS.**

### Lecturas de otros BCs

| Adapter | Query |
|---|---|
| `PaymentReader` | `payments` con `created_at` en el período (todos los estados), filtrado por `tenant_id` si el run no es global |
| `LedgerChecker` | `SUM(credit) - SUM(debit)` de `postings` unidos a `journal_entries` con `created_at` en el período |

Ambas consultas van directo al pool, sin fijar `app.current_tenant`.

---

## 9. Seguridad y multi-tenancy

- **Autenticación:** JWT o API key (grupo `protected`). Sin tenant → 401.
- **Autorización:** no hay guard de rol. Cualquier usuario del tenant puede crear y procesar runs
  y resolver discrepancias.
- **Aislamiento:** solo en la aplicación (`ownedBy` y el check de tenant de la discrepancia) y en
  el `WHERE tenant_id` de las consultas. No hay RLS de respaldo en las tablas propias.
- **RLS de las tablas leídas:** `payments`, `journal_entries` y `postings` tienen RLS por
  `app.current_tenant`, pero los readers no lo fijan. Hoy funciona porque el rol de conexión
  (`cobros`) es superuser y dueño de las tablas.
- **Atribución:** `resolved_by` lo envía el cliente en el body.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Severidad | Tema | Detalle |
|---|---|---|---|
| 1 | Alta | Sin guard de rol | `RegisterRoutes` no recibe guards: un usuario `read_only` puede crear runs, subir el CSV y resolver o ignorar discrepancias. |
| 2 | Alta | `resolved_by` falsificable | Se toma del body en lugar del usuario autenticado. Tampoco hay evento de resolución, así que Audit no registra quién cerró cada discrepancia. |
| 3 | Alta | Procesamiento sin atomicidad ni control de concurrencia | `Update` no condiciona el estado previo: dos requests simultáneos sobre el mismo run `pending` lo procesan ambos (discrepancias duplicadas, contadores pisados). Discrepancias, run y outbox se escriben fuera de una transacción. El error de `Publish` se descarta (el evento puede perderse). Si falla el `Update` final, las discrepancias quedan guardadas y el run queda `running` para siempre, sin forma de reintentarlo. `ProcessInternal` además ignora el error del `Update` a `running`. |
| 4 | Alta | RLS | Las tablas `reconciliation_*` no tienen RLS (es el único BC con datos por tenant sin ella). Los readers consultan `payments` y `postings` sin sesión RLS: con un rol sin `BYPASSRLS`, `ReadByPeriod` devolvería 0 filas (todo el CSV saldría `missing_in_system`) y `CheckBalance` devolvería 0 (Ledger "balanceado" en falso). |
| 5 | Media | Tipo de run no validado | Se puede subir un CSV a un run `internal_ledger` o pedir `process-internal` sobre un run `payment`: ambos se ejecutan y completan el run con el tipo equivocado. |
| 6 | Media | Matching incompleto | No compara moneda (mismo monto en otra moneda = match). Solo 3 pares de estado son críticos: `processing`↔`captured` o `refunded`↔`captured` pasan como match. Referencias duplicadas (en el CSV o en `payments`) se pisan en silencio. Los pagos se filtran por `created_at`, pero el CSV no se filtra por período: los bordes generan `missing_*` falsos. `total_records` cuenta pagos sin `psp_reference` que nunca se comparan. |
| 7 | Media | Errores de cliente como 500 | IDs con formato inválido, `type` inválido y `action` inválida responden 500. Un `amount` no entero en el CSV también da 500 y, como se detecta después de `Start`, deja el run `failed` sin reintento posible. Un `status` de filtro inválido devuelve la lista vacía. |
| 8 | Media | Chequeo del Ledger débil | El trigger de la base ya garantiza el balance de cada asiento, así que el chequeo casi no puede fallar. Además suma todas las monedas juntas. No cruza Ledger contra pagos capturados. |
| 9 | Baja | `limit` sin tope | `GET /runs` y `GET /runs/:runID` aceptan cualquier `limit`. |
| 10 | Baja | Síncrono con 202 | El CSV (hasta 50 MB) se procesa dentro del request y la respuesta es 202 aunque el trabajo ya terminó. |
| 11 | Baja | Dominio y limpieza | `Complete` y `Fail` no validan el estado previo. `Fail` no emite evento. El dominio usa `time.Now()`. Hay código muerto: `ErrRunNotCompleted`, el port `TxManager` y el campo `runRepo TxManager` de `StartReconciliationUseCase`. `processed_at` se parsea pero no se usa. `DiscrepancyRepo.FindByID` usa el pool en lugar de `ConnFromContext`. |
| 12 | Producto | Alcance | Sin runs programados ni ingesta automática del informe. Sin conciliación de payouts (`unknown`). Sin runs globales vía API. Un único formato de CSV. |

---

## 11. Testing

`go test ./context/reconciliation/...` (100 % de cobertura de sentencias en los cinco paquetes).

| Paquete | Enfoque |
|---|---|
| `domain` | Máquina de estados del run, validación de período, `Resolve`/`Ignore`, VOs y eventos. |
| `application` | Mocks de repos, reader, parser y publisher: cada tipo de discrepancia, `ownedBy` entre tenants y runs globales, fallos que marcan `failed`, resolución. |
| `http` | Binding, 401 sin tenant, mapeo de errores a 400/404/409/500. |
| `postgres` | pgxmock: SQL de cada método, `ErrNoRows` → not found, nulos. |
| `readers` | Parser CSV (encabezados flexibles, filas mal formadas, `processed_at` opcional) y SQL de `PaymentReader`/`LedgerChecker` con y sin tenant. |

No hay tests contra una base real ni de concurrencia.

---

## 12. Mapa de código

```
context/reconciliation/
├── domain/
│   ├── run.go              # agregado ReconciliationRun: pending→running→completed/failed
│   ├── discrepancy.go      # entidad Discrepancy: open→resolved/ignored
│   ├── events.go           # ReconciliationCompletedEvent
│   ├── vo.go               # IDs, ReconciliationType, RunStatus, DiscrepancyType/Status
│   └── errors.go
├── application/
│   ├── ports.go            # RunRepository, DiscrepancyRepository, PaymentReader, ReportParser, LedgerChecker, EventPublisher
│   ├── commands.go         # comandos, queries y views
│   ├── start_reconciliation.go
│   ├── process_report.go   # ProcessReportUseCase (matching) + ProcessInternalUseCase
│   └── resolve_discrepancy.go # Resolve + GetReport + ListRuns
└── infrastructure/adapters/
    ├── inbound/http/handler.go          # 6 endpoints + RegisterRoutes
    └── outbound/
        ├── postgres/run_repo.go, discrepancy_repo.go
        └── readers/
            ├── csv_parser.go            # CSVReportParser
            └── payment_reader.go        # PaymentReader (payments) + LedgerChecker (postings)
```

Wiring: `cmd/api/main.go` (repos, readers, casos de uso y rutas). El worker no tiene nada de
Reconciliation: el evento lo consume Audit.
