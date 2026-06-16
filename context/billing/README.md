# context/billing — Billing & Fees

Gestiona los planes de tarifas de la plataforma y calcula la comisión real de cada pago. Reemplaza el `FixedRateCalculator` placeholder de Payment Processing con un sistema de planes configurable por tenant.

---

## Responsabilidades

- Mantener el catálogo de **PricingPlans** (planes de tarifas)
- Asignar un plan a cada tenant con posibilidad de **overrides negociados**
- Calcular la comisión exacta para cada pago según el plan activo del tenant
- Exponer una API para que el operador gestione planes y asignaciones

---

## Estructura

```
context/billing/
├── domain/
│   ├── errors.go       # 11 errores de negocio
│   ├── vo.go           # PlanID, TenantPlanID, Money, PaymentMethod, MethodRate, FeeBreakdown
│   ├── events.go       # PlanCreatedEvent, PlanAssignedEvent
│   ├── plan.go         # Agregado PricingPlan + CalculateFee()
│   └── tenant_plan.go  # Entidad TenantPlan + CalculateFee() con overrides
├── application/
│   ├── ports.go        # PlanRepository, TenantPlanRepository, EventPublisher, TxManager
│   ├── commands.go     # DTOs + PlanView, TenantPlanView, CalculateFeeResult
│   ├── create_plan.go
│   ├── assign_plan.go
│   ├── calculate_fee.go   # Caso de uso central — usado por Payment Processing
│   └── get_plans.go       # GetPlan, ListPlans, GetTenantPlan
└── infrastructure/adapters/
    ├── inbound/http/
    │   └── billing_handler.go  # Endpoints de gestión y consulta
    └── outbound/postgres/
        ├── plan_repo.go          # Plans + method rates en tablas separadas
        └── tenant_plan_repo.go
```

---

## Dominio

### `PricingPlan`

Define las tarifas que aplican a los comercios que usan este plan.

**Cálculo de comisión (basis points, nunca float):**

```
fee = ceil(amount × rate_bps / 10_000) + fixed_amount
```

| Plan | RateBps | FixedAmount | Pago $1.000 | Comisión |
|---|---|---|---|---|
| Básico | 300 | 0 | $1.000 | $30.00 |
| Pro | 250 | 50 | $1.000 | $25.50 |
| Enterprise | 150 | 0 | $1.000 | $15.00 |

Overrides por método de pago: un mismo plan puede tener tasa distinta para tarjeta vs billetera.

### `TenantPlan`

Asignación de un plan a un tenant. Permite tarifas negociadas individuales:

```
Prioridad de resolución (mayor → menor):
  1. TenantPlan.customRateBps / customFixedAmount  ← tarifa negociada
  2. PricingPlan.methodRates[method]               ← override por método
  3. PricingPlan.baseRateBps / baseFixedAmount     ← tarifa base
  4. Fallback interno (300 bps)                    ← sin plan asignado
```

Solo puede haber **un TenantPlan activo** por tenant. Al asignar uno nuevo, el anterior se desactiva dentro de la misma transacción.

---

## Caso de uso central: `CalculateFee`

`Payment Processing` llama a este caso de uso (vía `BillingFeeCalculator`) en cada pago:

```
ProcessPayment
  → BillingFeeCalculator.Calculate(ctx, tenantID, amount, method)
  → CalculateFeeUseCase.Execute(ctx, CalculateFeeQuery{...})
      → TenantPlanRepo.FindActive(tenantID)
      → PlanRepo.FindByID(tenantPlan.PlanID)
      → tenantPlan.CalculateFee(plan, amount, currency, method)
      → retorna CalculateFeeResult{FeeAmount, RateBpsApplied, PlanID, ...}
```

El resultado incluye el desglose completo de la comisión para trazabilidad.

---

## API

```
POST /api/v1/billing/plans                     Crear plan          [platform_support]
GET  /api/v1/billing/plans                     Listar planes       [platform_support]
GET  /api/v1/billing/plans/:planID             Consultar plan      [platform_support]
POST /api/v1/billing/tenants/:tenantID/plan    Asignar plan        [platform_support]
GET  /api/v1/billing/my-plan                   Mi plan activo      [admin]
```

### Crear un plan con overrides por método

```json
POST /api/v1/billing/plans
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

### Asignar plan con tarifa negociada

```json
POST /api/v1/billing/tenants/:tenantID/plan
{
  "plan_id": "...",
  "custom_rate_bps": 200
}
```

El `custom_rate_bps: 200` (2.00%) sobreescribe el `base_rate_bps` del plan para este tenant específico.

---

## Integración con Payment Processing

`BillingFeeCalculator` en `context/payment/infrastructure/adapters/outbound/fees/` implementa el puerto `FeeCalculator` de Payment y delega en `CalculateFeeUseCase`:

```go
// cmd/api/main.go
calculateFeeUC := billingapp.NewCalculateFeeUseCase(planRepo, tenantPlanRepo, 300)
feeCalculator  := fees.NewBillingFeeCalculator(calculateFeeUC)
processPayment := paymentapp.NewProcessPaymentUseCase(..., feeCalculator, ...)
```

Payment Processing no conoce ni importa nada de Billing. Solo conoce el puerto `FeeCalculator`.

---

## Dependencias del contexto

```
Produce eventos → stream BILLING (billing.plan.created.v1, billing.plan.assigned.v1)
Consume eventos → ninguno

Es consultado por → context/payment (vía BillingFeeCalculator, mismo proceso)

Depende de → pkg/postgres
             pkg/outbox (EventPublisher)
```
