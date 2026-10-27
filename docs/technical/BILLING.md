# Billing & Fees — Documentación técnico-funcional

> **Bounded Context:** `context/billing`
> **Migración:** [`000008_billing_init`](../../migrations/000008_billing_init.up.sql)
> **Stream NATS:** `BILLING` (`billing.>`)

---

## 1. Propósito

Billing es el contexto que fija **cuánto cobra la plataforma** por cada transacción. Gestiona:

- El catálogo de **`PricingPlan`** (planes de tarifas): tasa base en basis points, monto fijo, suscripción mensual y overrides por método de pago.
- La **asignación de planes a tenants** (`TenantPlan`), con posibilidad de **tarifas negociadas** individuales.
- El **cálculo determinístico de la comisión** (`CalculateFee`) que consume Payment Processing en cada cobro.

Reemplaza al `FixedRateCalculator` placeholder de la Fase 2: hoy Payment cablea `fees.BillingFeeCalculator`, que delega in-process en `billing.CalculateFeeUseCase` (ver §5.3 y §9).

### Estado actual de las integraciones

| Puerto / dependencia | Implementación cableada | Comentario |
|---|---|---|
| `PlanRepository` | `billingpg.NewPlanRepository(pool)` | Tablas `billing_plans` + `billing_plan_method_rates`. |
| `TenantPlanRepository` | `billingpg.NewTenantPlanRepository(pool)` | Tabla `billing_tenant_plans` con RLS. |
| `EventPublisher` | `outbox.NewEventPublisher[billingdomain.Event]` | Publicación transaccional vía Outbox (stream `BILLING`). |
| `TxManager` | Compartido con el resto de contextos (`cmd/api/main.go`) | Propaga `app.current_tenant` para RLS. |
| Fallback de tarifa | `NewCalculateFeeUseCase(..., billingapp.DefaultFallbackRateBps)` en `cmd/api/main.go` | 300 bps (3.00%) cuando el tenant no tiene plan asignado. Se loguea como warning en cada cobro. |
| Autorización | `RouteGuards` con `authttp.RequireRole(...)` en `cmd/api/main.go` | Gestión de planes y asignaciones: `platform_support`. Lectura del catálogo y `my-plan`: `admin` o `platform_support`. |

### Fuera de alcance (hoy)

- **Facturación de la suscripción mensual:** `monthly_fee` se almacena, se edita y se expone, pero no existe caso de uso que la cobre ni la refleje en Ledger (requiere job periódico + asiento en Ledger).
- **Cambio de tarifa retroactivo:** los pagos ya capturados conservan la comisión con la que se procesaron; no hay recálculo ni notas de crédito de comisiones.
- **Consumidores de eventos `billing.*`:** nadie (ni Ledger, ni Reporting, ni Notification, ni Webhook) suscribe los eventos `billing.plan.*`.
- **Planes multi-moneda / conversión FX:** un plan es mono-moneda; un cobro en otra moneda paga solo la tasa proporcional (ver §4.4).
- **Overrides por volumen** (tarifas escalonadas por tramos).

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **PricingPlan** (`plan`) | Agregado raíz. Define tarifa base (`base_rate_bps` + `base_fixed_amount`), overrides por método y suscripción mensual (`monthly_fee`). Global de la plataforma, sin tenant. |
| **Basis point (bps)** | 1 bps = 0.01 %. Rango válido 0–10000 (0 %–100 %). Toda la aritmética de comisiones usa enteros (`int64`), nunca float. |
| **Method rate** | Override de tarifa para un método de pago concreto dentro de un plan (`card`, `wallet`, `transfer`, `qr`). Reemplaza *ambos* componentes base (tasa y fijo). |
| **TenantPlan** | Asignación de un plan a un tenant, con snapshot del nombre (`plan_name`) y overrides negociados opcionales (`custom_rate_bps`, `custom_fixed_amount`). Solo uno activo por tenant. |
| **Override negociado** | Tarifa personalizada para un tenant específico. Prevalece sobre cualquier tarifa del plan (base o por método). |
| **FeeBreakdown** | Desglose de cómo se calculó una comisión: tasa aplicada, parte proporcional, parte fija, total, plan y flags (`MethodOverride`, `CurrencyMismatch`). |
| **Fallback** | Tarifa interna (`FallbackRateBps`, cableado en `DefaultFallbackRateBps` = 300 bps) que aplica cuando el tenant no tiene plan asignado. `PlanID` reportado: `"fallback"`, `Fallback=true`. |
| **Grandfathering** | Un plan desactivado del catálogo no admite nuevas asignaciones, pero sigue aplicándose a los tenants que ya lo tenían hasta que se los reasigne. |
| **`platform_fee`** | Nombre que recibe en Payment/Ledger el resultado de `CalculateFeeResult.FeeAmount`. |

---

## 3. Flujo funcional

```mermaid
sequenceDiagram
    autonumber
    actor P as Operador (platform_support)
    actor T as Comercio (admin)
    participant B as Billing API
    participant DB as PostgreSQL
    participant O as Outbox → NATS (BILLING)
    participant PAY as Payment Processing

    P->>B: POST /billing/plans {name, base_rate_bps, method_rates…}
    B->>DB: tx{INSERT billing_plans + billing_plan_method_rates}
    B->>O: billing.plan.created.v1
    B-->>P: 201 {plan_id}

    P->>B: POST /billing/tenants/:tenantID/plan {plan_id, custom_rate_bps?}
    B->>DB: FindByID(plan) → ¿activo? (si no → 409)
    B->>DB: FindActive(tenant) → plan anterior (si existe)
    B->>DB: tx{UPDATE anterior active=false + INSERT nuevo + outbox}
    B->>O: billing.plan.assigned.v1
    B-->>P: 200 {tenant_plan_id, plan_name}

    T->>B: GET /billing/my-plan
    B-->>T: 200 TenantPlanView (o vista fallback si no tiene plan)

    Note over PAY,B: En cada cobro (mismo proceso, llamada in-process)
    PAY->>B: CalculateFeeQuery{tenant, amount, currency, method}
    B->>DB: FindActive(tenant) + FindByID(plan)
    alt sin plan asignado
        B-->>PAY: fallback 300 bps (ceil) + log WARN
    else plan asignado (activo o desactivado)
        B-->>PAY: FeeBreakdown con precedencia de tarifas (§4.4)
    end
```

---

## 4. Modelo de dominio

### 4.1 Agregado `PricingPlan`

Archivo: [domain/plan.go](../../context/billing/domain/plan.go)

| Campo | Invariantes |
|---|---|
| `id` (`PlanID`) | UUID generado por la aplicación (`NewPlanID`). |
| `name` | No vacío (`ErrPlanNameEmpty`). |
| `baseRateBps` | 0–10000 (`ErrInvalidRateBps`). |
| `baseFixedAmount` | ≥ 0, en centavos (`ErrInvalidFixedAmount`). |
| `methodRates` | `map[PaymentMethod]MethodRate`; cada entrada validada igual que la base (`AddMethodRate`). |
| `monthlyFee` | ≥ 0 (`ErrInvalidMonthlyFee`). Se guarda y edita (`SetMonthlyFee`), no se factura. |
| `currency` | ISO 4217 de 3 letras (`ErrInvalidCurrency`). Un plan es mono-moneda; los montos fijos están denominados en ella. |
| `active` | `NewPricingPlan` nace activo; `Deactivate()` lo apaga (`DELETE /plans/:id`). Inactivo = cerrado a nuevas asignaciones y a ediciones. |

`NewPricingPlan` registra `PlanCreatedEvent` (evento de plataforma: `tenantID` vacío). `ReconstitutePricingPlan` reconstruye desde persistencia **sin** emitir eventos.

Mutaciones: `UpdateBaseRates`, `SetMonthlyFee`, `AddMethodRate`, `RemoveMethodRate`, `Deactivate`. Los eventos `PlanUpdatedEvent` / `PlanDeactivatedEvent` los registra la capa de aplicación (`RecordUpdated` / `RecordDeactivated`) una vez validada la mutación completa.

### 4.2 Entidad `TenantPlan`

Archivo: [domain/tenant_plan.go](../../context/billing/domain/tenant_plan.go)

- Referencia al plan (`planID`) + **snapshot** de `planName` al momento de asignar (para reportes históricos aunque el plan cambie o se renombre).
- `customRateBps` / `customFixedAmount`: punteros; `nil` = sin override (la capa HTTP envía `-1` para indicar "sin override"). Validados en el mismo rango que la base.
- Rechaza planes inactivos (`ErrPlanInactive`) al crear la asignación.
- `Deactivate()` fija `active=false` y `validUntil=now()`.
- Registra `PlanAssignedEvent` con el `tenantID`.

### 4.3 Value Objects

Archivo: [domain/vo.go](../../context/billing/domain/vo.go)

| VO | Notas |
|---|---|
| `PlanID`, `TenantPlanID`, `TenantID` | Wrapper de UUID. |
| `PaymentMethod` | Duplicado intencionalmente del tipo de Payment para mantener el aislamiento entre contextos: `card`, `wallet`, `transfer`, `qr`. |
| `Money` | Monto en unidades mínimas (centavos) + moneda. `NewMoney` rechaza negativos y monedas ≠ 3 letras. `ReconstituteMoney` evita validación en re-hidratación. |
| `MethodRate` | `{RateBps, FixedAmount}` de un override por método. |
| `FeeBreakdown` | Resultado del cálculo: `RateBpsApplied`, `RateAmount`, `FixedAmount`, `TotalFee Money`, `PlanID`, `MethodOverride bool`, `CurrencyMismatch bool`. |

### 4.4 Cálculo de comisión — precedencia de tarifas

```
fee = ceil(amount × rate_bps / 10_000) + fixed_amount
```

El `ceil` se implementa con enteros: `(amount×rateBps + 9999) / 10000` — nunca redondea en favor del comercio.

Resolución de `(rate_bps, fixed_amount)`, de mayor a menor precedencia:

| # | Fuente | Efecto |
|---|---|---|
| 1 | `TenantPlan.customRateBps` / `customFixedAmount` | Reemplazan **solo la parte que personalizan**; si solo hay `custom_rate_bps`, el `fixed_amount` sigue viniendo del breakdown del plan. Anula el flag `MethodOverride` (queda `false`). |
| 2 | `PricingPlan.methodRates[method]` | Reemplaza **ambos** componentes base. Marca `MethodOverride=true`. |
| 3 | `PricingPlan.baseRateBps` / `baseFixedAmount` | Tarifa default del plan para todos los métodos. |
| 4 | Fallback interno (`FallbackRateBps`, 300 bps) | Solo si el tenant no tiene plan asignado. Un plan desactivado después de asignar **se sigue aplicando** (grandfathering). `PlanID="fallback"`, `fixed=0`. |

Validación previa en `PricingPlan.CalculateFee`: `amount > 0`.

**Moneda distinta a la del plan:** el pago no falla. La tasa proporcional (bps) es agnóstica de moneda y se aplica igual (incluido el `custom_rate_bps` negociado); los montos fijos —del plan, del method rate o el `custom_fixed_amount`— están denominados en la moneda del plan y, sin conversión FX, **se omiten**. El breakdown marca `CurrencyMismatch=true`, la comisión se expresa en la moneda del pago y `CalculateFee` loguea un warning.

Ejemplos (plan Pro: base 250 bps + 50 fijo; card 280/50; wallet 200/0):

| Pago | Método | Condición | Comisión |
|---|---|---|---|
| $1.000 (100.000 ¢) | card | sin override → method rate 280 bps + 50 | 2.800 + 50 = **2.850 ¢** ($28,50) |
| $1.000 | wallet | sin override → method rate 200 bps + 0 | **2.000 ¢** ($20,00) |
| $1.000 | card | con `custom_rate_bps=200` → fixed 50 heredado del breakdown | 2.000 + 50 = **2.050 ¢** ($20,50) |
| $1.000 | qr | sin method rate → base 250 bps + 50 | 2.500 + 50 = **2.550 ¢** ($25,50) |
| US$1.000 (plan ARS) | card | moneda distinta → solo method rate 280 bps, fijo omitido | **2.800 ¢** (US$28,00) |

### 4.5 Errores de dominio

Archivo: [domain/errors.go](../../context/billing/domain/errors.go) — 10 sentinelas:

| Error | Cuándo | HTTP |
|---|---|---|
| `ErrPlanNotFound` | Plan inexistente (repo mapea `pgx.ErrNoRows` a este error) | 404 |
| `ErrTenantPlanNotFound` | Tenant sin plan activo. `CalculateFee` y `GetTenantPlan` lo traducen a fallback; no llega al cliente desde `my-plan` | 404 |
| `ErrPlanInactive` | Asignar o editar un plan desactivado | 409 |
| `ErrPlanNameEmpty`, `ErrInvalidRateBps`, `ErrInvalidFixedAmount`, `ErrInvalidMonthlyFee`, `ErrInvalidPaymentMethod` | Validaciones de invariantes | 400 |
| `ErrInvalidCurrency` | Moneda ≠ 3 letras | 400 |
| `ErrFeeCalculationInvalid` | Overflow defensivo (`totalFee < 0`); inalcanzable en la práctica | 500 |

### 4.6 Eventos

Archivo: [domain/events.go](../../context/billing/domain/events.go). Se publican **por Outbox** en la misma transacción que la escritura, stream `BILLING`.

| Subject | Emisor | Payload |
|---|---|---|
| `billing.plan.created.v1` | `NewPricingPlan` (vía `CreatePlan`) | `plan_id`, `plan_name`, `rate_bps` (+ `event_id`, `occurred_at`; `tenant_id` vacío: evento de plataforma) |
| `billing.plan.assigned.v1` | `NewTenantPlan` (vía `AssignPlan`) | `tenant_plan_id`, `tenant_id`, `plan_id`, `plan_name` |
| `billing.plan.updated.v1` | `RecordUpdated` (vía `UpdatePlan`) | `plan_id`, `plan_name`, `rate_bps` |
| `billing.plan.deactivated.v1` | `RecordDeactivated` (vía `DeactivatePlan`, solo si estaba activo) | `plan_id`, `plan_name` |

Ningún contexto consume estos subjects hoy (§1, fuera de alcance). El worker arranca consumers de otros streams; Billing no aporta ninguno.

---

## 5. Casos de uso

Archivos en [application/](../../context/billing/application). Puertos en `ports.go`: `PlanRepository`, `TenantPlanRepository`, `EventPublisher`, `TxManager`.

### 5.1 `CreatePlan`

`CreatePlanCmd{Name, Description, BaseRateBps, BaseFixedAmount, MonthlyFee, Currency, MethodRates[]}`

1. `domain.NewPricingPlan` valida invariantes y genera el evento.
2. Por cada `MethodRateInput`: `ParsePaymentMethod` + `AddMethodRate` (un método inválido aborta todo: `"method rate %q: …"`).
3. Tx: `planRepo.Save` (insert plan + upsert de method rates) y `publisher.Publish(events)`.

Resultado: `{plan_id}`. No hay deduplicación por nombre: dos planes pueden llamarse igual.

### 5.2 `AssignPlan`

`AssignPlanCmd{TenantID, PlanID, CustomRateBps, CustomFixedAmount, ValidFrom}` (con `-1` = sin override; `ValidFrom` zero → `now()`).

1. Parse de IDs; `planRepo.FindByID` → debe existir y estar activo.
2. `tenantPlanRepo.FindActive(tenant)` captura el plan anterior (si existe).
3. `domain.NewTenantPlan` valida y genera `PlanAssignedEvent`.
4. Tx: desactivar anterior (`Update`: `active=false`, `valid_until=now`) → `Save` del nuevo → publicar evento.

La unicidad "un plan activo por tenant" está además garantizada en BD por el índice parcial único `idx_billing_tenant_plans_active` (una carrera entre dos asignaciones falla por `UNIQUE` → 500).

Resultado: `{tenant_plan_id, plan_name}`.

### 5.3 `CalculateFee` — el caso de uso central

Entrada: `CalculateFeeQuery{TenantID, Amount, Currency, PaymentMethod}`. Salida: `CalculateFeeResult` con desglose completo (`FeeAmount`, `RateBpsApplied`, `RateAmount`, `FixedAmount`, `PlanID`, `PlanName`, `MethodOverride`, `TenantOverride`, `CurrencyMismatch`, `Fallback`).

```
Execute
  ├─ valida tenantID (UUID), método (∈ card|wallet|transfer|qr) y amount > 0
  ├─ TenantPlanRepo.FindActive(tenant)
  │    ├─ ErrTenantPlanNotFound → WARN + calculateFallback (300 bps por defecto)
  │    └─ otro error → aborta (Payment reintenta el cobro más tarde)
  ├─ PlanRepo.FindByID(tp.PlanID)   (activo o inactivo: grandfathering)
  └─ tp.CalculateFee(plan, amount, currency, method) → breakdown → result
       └─ CurrencyMismatch → WARN (solo bps, fijos omitidos)
```

Detalles:
- `TenantOverride = true` si el tenant tiene cualquiera de los dos customs.
- El constructor normaliza `fallbackRateBps <= 0` a `DefaultFallbackRateBps` (300).
- Loguea con `slog.Default()` (lo configura `cmd/api`).
- Es **lectura pura**: no abre transacción ni publica eventos.
- Payment solo consume `FeeAmount` + `Currency`; el resto del desglose queda disponible para auditoría/reportes futuros.

### 5.4 `UpdatePlan` y `DeactivatePlan`

Archivo: [update_plan.go](../../context/billing/application/update_plan.go).

- **`UpdatePlan`** (`UpdatePlanCmd`): PATCH sobre `base_rate_bps`, `base_fixed_amount` y `monthly_fee` (punteros: `nil` = no tocar). `MethodRates` no-nil **reemplaza el conjunto completo** de overrides (los omitidos se eliminan; el repo borra y reinserta). Valida todo antes de mutar; rechaza planes inactivos (`ErrPlanInactive`). Tx: `Update` + `billing.plan.updated.v1`.
- **`DeactivatePlan`**: idempotente. Si el plan estaba activo, lo desactiva y publica `billing.plan.deactivated.v1` en la misma tx. No toca las asignaciones existentes (grandfathering): para cambiar la tarifa de esos tenants hay que reasignarlos.

### 5.5 Consultas: `GetPlan`, `ListPlansPage`, `GetTenantPlan`, `ListTenantPlans`

Proyectan `PlanView` / `TenantPlanView` (snake_case en JSON). `PlanView` incluye `base_rate_percent` y `rate_percent` legibles (`formatRatePercent`: 250 → `"2.50%"`).

- `ListPlansPage`: paginado (`limit` default 20, máx 100; `offset`), ordenado por nombre; solo activos salvo `include_inactive=true`.
- `GetTenantPlan`: plan activo del tenant. Si no tiene, retorna la **vista fallback** (`fallback: true`, `fallback_rate_bps`) en vez de 404, porque es lo que `CalculateFee` le cobra realmente. Recibe el mismo `fallbackRateBps` que `CalculateFee`.
- `ListTenantPlans`: historial de asignaciones (activas e inactivas) paginado, `valid_from DESC`.

---

## 6. API HTTP

Archivo: [billing_handler.go](../../context/billing/infrastructure/adapters/inbound/http/billing_handler.go). Montada en el grupo `/api/v1` con `JWTMiddleware` de Auth.

La autorización la inyecta el composition root vía `RouteGuards` (para no acoplar Billing con Auth). Los 5 guards son **obligatorios**: `RegisterRoutes` entra en pánico si falta alguno, porque gin saltea los handlers `nil` y la ruta quedaría abierta a cualquier JWT válido.

| Método | Ruta | Guard → rol (`cmd/api`) | Éxito |
|---|---|---|---|
| `POST` | `/api/v1/billing/plans` | `ManagePlans` → `platform_support` | `201 {"plan_id": "…"}` |
| `PATCH` | `/api/v1/billing/plans/:planID` | `ManagePlans` → `platform_support` | `200 PlanView` |
| `DELETE` | `/api/v1/billing/plans/:planID` | `ManagePlans` → `platform_support` | `200 PlanView` (desactiva; idempotente) |
| `GET` | `/api/v1/billing/plans` | `ReadPlans` → `admin`, `platform_support` | `200 {"plans":[PlanView…], "page","limit","offset","total"}` |
| `GET` | `/api/v1/billing/plans/:planID` | `ReadPlans` → `admin`, `platform_support` | `200 PlanView` |
| `POST` | `/api/v1/billing/tenants/:tenantID/plan` | `AssignPlan` → `platform_support` | `200 {"tenant_plan_id","plan_name"}` |
| `GET` | `/api/v1/billing/tenants/:tenantID/plans` | `ReadTenantPlan` → `platform_support` | `200 {"tenant_plans":[TenantPlanView…], "page","limit","offset","total"}` |
| `GET` | `/api/v1/billing/my-plan` | `ReadMyPlan` → `admin`, `platform_support` | `200 TenantPlanView` |

Query params de los listados: `limit` (default 20, máx 100), `offset`; `GET /plans` acepta además `include_inactive=true`. Valores no numéricos o negativos → 400.

### 6.1 Crear plan — `POST /billing/plans`

```json
{
  "name": "Pro",
  "description": "Plan para comercios con alto volumen",
  "base_rate_bps": 250,
  "base_fixed_amount": 0,
  "monthly_fee": 500000,
  "currency": "ARS",
  "method_rates": [
    { "method": "card",   "rate_bps": 280, "fixed_amount": 50 },
    { "method": "wallet", "rate_bps": 200, "fixed_amount": 0  }
  ]
}
```

Requeridos (binding): `name`, `currency`. `base_rate_bps` puede ser `0` (plan sin comisión proporcional); los rangos los valida el dominio.

### 6.2 Editar plan — `PATCH /billing/plans/:planID`

```json
{ "base_rate_bps": 240, "method_rates": [ { "method": "card", "rate_bps": 270, "fixed_amount": 50 } ] }
```

Campos ausentes no se modifican. `method_rates`, si se envía (aunque sea `[]`), reemplaza el conjunto completo de overrides. Plan inactivo → 409.

### 6.3 Asignar plan — `POST /billing/tenants/:tenantID/plan`

```json
{ "plan_id": "…", "custom_rate_bps": 200 }
```

`custom_*` ausentes o `null` = sin override. Respuesta: `{"tenant_plan_id":"…","plan_name":"Pro"}`.

### 6.4 Mi plan — `GET /billing/my-plan`

El tenant sale del contexto JWT (`postgres.TenantIDFromContext`). Si nunca se le asignó plan, responde `200` con la tarifa que realmente se le cobra:

```json
{ "tenant_id": "…", "plan_name": "Fallback (sin plan asignado)", "active": true, "fallback": true, "fallback_rate_bps": 300 }
```

### Mapeo de errores (`respondDomainError`)

| Dominio | HTTP |
|---|---|
| `ErrPlanNotFound`, `ErrTenantPlanNotFound` | 404 |
| `ErrPlanInactive` | 409 |
| `ErrInvalidRateBps`, `ErrInvalidFixedAmount`, `ErrInvalidMonthlyFee`, `ErrPlanNameEmpty`, `ErrInvalidPaymentMethod`, `ErrInvalidCurrency` | 400 |
| Body inválido (binding) | 400 `invalid request body` |
| Resto (errores de repo/tx) | 500 `internal server error` |

No hay endpoint de cálculo de fee: `CalculateFee` se expone **solo in-process** a Payment (§9).

---

## 7. Eventos y consumidores

### 7.1 Producidos

| Subject | Trigger | Consumidores actuales |
|---|---|---|
| `billing.plan.created.v1` | `POST /billing/plans` | Ninguno con lógica de negocio. |
| `billing.plan.assigned.v1` | `POST /billing/tenants/:tenantID/plan` | Ninguno. |
| `billing.plan.updated.v1` | `PATCH /billing/plans/:planID` | Ninguno. |
| `billing.plan.deactivated.v1` | `DELETE /billing/plans/:planID` | Ninguno. |

Payloads según §4.6; entrega garantizada vía Outbox + relay (`pkg/outbox`), dedupe JetStream por `Nats-Msg-Id`.

### 7.2 Consumidos

Ninguno. Billing no tiene adapters inbound NATS.

### 7.3 Lecturas directas

Ningún contexto lee las tablas `billing_*` directamente. La única integración activa es la llamada in-process de Payment (§9).

---

## 8. Persistencia

Migración [000008_billing_init](../../migrations/000008_billing_init.up.sql):

| Tabla | Propósito | Columnas clave |
|---|---|---|
| `billing_plans` | Catálogo global (**sin RLS**) | `id PK`, `name`, `description`, `base_rate_bps CHECK 0–10000`, `base_fixed_amount ≥0`, `monthly_fee ≥0`, `currency CHAR(3)`, `active`, `created_at`, `updated_at` |
| `billing_plan_method_rates` | Overrides por método | PK compuesto `(plan_id, method)`; `method IN ('card','wallet','transfer','qr')`; FK `ON DELETE CASCADE`; `rate_bps CHECK 0–10000`; `fixed_amount ≥0` |
| `billing_tenant_plans` | Asignaciones (**con RLS** por `tenant_id`) | `id PK`, `tenant_id`, `plan_id FK`, `plan_name` (snapshot), `custom_rate_bps`/`custom_fixed_amount` (nullable, mismos CHECKs), `active`, `valid_from`, `valid_until`, `created_at` |

- **Unicidad funcional:** índice parcial único `ON billing_tenant_plans (tenant_id) WHERE active = true`.
- Índice de historial: `(tenant_id, valid_from DESC)`.
- RLS de `billing_tenant_plans` viaja con `app.current_tenant` que pone el `TxManager` (igual que el resto de contextos; no filtra mientras la app conecte como owner).
- `FindActive` resuelve con `WHERE tenant_id=$1 AND active=true ORDER BY valid_from DESC LIMIT 1`.
- `PlanRepository.Update` reescribe tarifas + `monthly_fee` + `active` y **reemplaza** los method rates (DELETE + INSERT dentro de la misma tx: los overrides retirados del agregado se borran). `TenantPlanRepository.Update` solo toca `active` y `valid_until`.
- `ListPage` / `ListByTenantPage` hacen `COUNT(*)` + `LIMIT/OFFSET` para devolver `total`.

---

## 9. Integración con Payment Processing

El puerto `FeeCalculator` de Payment se satisface con `fees.BillingFeeCalculator` ([billing_calculator.go](../../context/payment/infrastructure/adapters/outbound/fees/billing_calculator.go)), que arma un `CalculateFeeQuery` con el tenant/monto/moneda/método del pago y devuelve `Money{FeeAmount, Currency}`:

```go
// cmd/api/main.go
calculateFeeUC  := billingapp.NewCalculateFeeUseCase(planRepo, tenantPlanRepo, 300)
feeCalculator   := fees.NewBillingFeeCalculator(calculateFeeUC)
processPayment  := paymentapp.NewProcessPaymentUseCase(..., feeCalculator, ...)
```

Reglas de frontera:

- **Dirección de dependencia:** el adaptador vive en `context/payment` e importa `context/billing/application`. El dominio y application de Payment no conocen Billing; Billing no conoce Payment (duplica `PaymentMethod` a propósito).
- **Síncrono, mismo proceso:** si `CalculateFee` falla (p. ej. error de BD), `ProcessPayment` marca el pago `failed` con error de fee; no hay reintento automático. La falta de plan y la moneda distinta **no** fallan el cobro (fallback y solo-bps, respectivamente).
- `FixedRateCalculator` (tasa fija global, mínimo de 1 ¢) sigue en el paquete pero **no está cableado**.
- La comisión calculada viaja al Ledger como `platform_fee` dentro de `payment.captured.v1`; Billing no participa del asiento.

---

## 10. Testing

| Capa | Archivos | Cobertura principal |
|---|---|---|
| Dominio | `plan_test.go`, `tenant_plan_test.go`, `vo_test.go`, `events_test.go` | Invariantes, redondeo ceil, override por método, precedencia de customs, plan mismatch, moneda distinta (solo bps, fijos omitidos), tipos/timestamps de eventos. |
| Aplicación | `create_plan_test.go`, `assign_plan_test.go`, `calculate_fee_test.go`, `get_plans_test.go` (+ `mocks_test.go`) | Éxito, validaciones, propagación de errores de repo/publisher/tx, desactivación del anterior, fallback por ausencia de plan, grandfathering de plan inactivo, moneda distinta, default 300, paginación, vista fallback de `GetTenantPlan`. |
| HTTP | `billing_handler_test.go` (+ `helpers_test.go`) | Happy paths, PATCH/DELETE, listados paginados, mapeo 400/404/409 de `respondDomainError`, autorización por rol (`RouteGuards`) y pánico sin guards. |
| Postgres | `plan_repo_test.go`, `tenant_plan_repo_test.go` (+ `setup_test.go`) | pgxmock: save/find/update, reemplazo de method rates, scan de NULLs en customs. |
| Cruzado | `context/payment/.../fees/fees_test.go` | `BillingFeeCalculator` con el use case real de Billing + repos fake (camino de fallback). |

```bash
go test ./context/billing/... ./context/payment/...
make test   # go test -race -count=1 ./...
```

Faltan: tests de concurrencia en `AssignPlan` (índice único) y moneda distinta end-to-end desde Payment.

---

## 11. Mapa de código

```
context/billing/
├── domain/
│   ├── plan.go          # Agregado PricingPlan + CalculateFee (ceil, method rates)
│   ├── tenant_plan.go   # TenantPlan + CalculateFee con overrides negociados
│   ├── vo.go            # PlanID, TenantPlanID, Money, PaymentMethod, MethodRate, FeeBreakdown
│   ├── events.go        # billing.plan.{created,assigned,updated,deactivated}.v1
│   └── errors.go        # 10 errores de negocio
├── application/
│   ├── ports.go         # PlanRepository, TenantPlanRepository, EventPublisher, TxManager, Page
│   ├── commands.go      # Cmd/Query DTOs + PlanView, TenantPlanView, CalculateFeeResult
│   ├── create_plan.go
│   ├── assign_plan.go
│   ├── calculate_fee.go # Caso de uso central (fallback 300 bps, grandfathering, moneda distinta)
│   ├── update_plan.go   # UpdatePlan, DeactivatePlan, ListTenantPlans
│   └── get_plans.go     # GetPlan, ListPlansPage, GetTenantPlan (vista fallback)
└── infrastructure/adapters/
    ├── inbound/http/billing_handler.go       # 8 rutas + RouteGuards + respondDomainError
    └── outbound/postgres/
        ├── plan_repo.go         # billing_plans + billing_plan_method_rates (reemplazo), ListPage
        └── tenant_plan_repo.go  # billing_tenant_plans (FindActive/ListByTenant/ListByTenantPage)

Integración saliente:
context/payment/infrastructure/adapters/outbound/fees/billing_calculator.go  # FeeCalculator → CalculateFeeUseCase
Cableado: cmd/api/main.go, sección "Billing & Fees" (grupo protected, RouteGuards por rol, fallback 300 bps)
```
