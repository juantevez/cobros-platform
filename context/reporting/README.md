# Reporting (Dashboard & Reporting)

Contexto de **lectura (CQRS read-model)**. A diferencia del resto de los
contextos, Reporting no tiene agregados, invariantes de dominio ni mueve dinero:
consume eventos de otros contextos y construye proyecciones desnormalizadas
optimizadas para las consultas del dashboard.

## Flujo

```
NATS JetStream
  ├── PAYMENT  (payment.captured.v1)   ─┐
  └── LEDGER   (ledger.entry.posted.v1) │
                                        ▼
             inbound/nats/EventConsumer  (cmd/worker)
                                        ▼
             application/ProjectEventsUseCase
                                        ▼
             outbound/postgres/ProjectionRepository
                report_payment_fact       (1 fila por pago)
                report_ledger_movement    (1 fila por posting)
                                        ▼
             application/GetReportsUseCase  ← inbound/http  (cmd/api)
                agrega en tiempo de consulta
```

## Idempotencia

No se usan contadores incrementales: una re-entrega de un evento los
duplicaría. En su lugar se guardan **hechos inmutables** con inserción
idempotente:

- `report_payment_fact` — PK `payment_id`
- `report_ledger_movement` — PK `(entry_id, account_id, direction)`

Ambas con `ON CONFLICT DO NOTHING`. Las métricas se **agregan al consultar**
(`date_trunc`, `SUM ... FILTER`), no al proyectar.

## Denormalización de `account_type`

El evento `ledger.entry.posted.v1` trae `account_id` por posting pero no el
tipo de cuenta. `AccountReader` lo resuelve leyendo `ledger_accounts` (misma BD,
patrón de lectura cruzada ya usado por reconciliation/notification) y lo
denormaliza en `report_ledger_movement` para agregar balances sin JOINs.

## Endpoints

| Método | Ruta | Descripción |
|---|---|---|
| GET | `/api/v1/reports/volume?from=&to=&granularity=` | Serie de volumen transaccional |
| GET | `/api/v1/reports/revenue?from=&to=` | Comisiones cobradas por período |
| GET | `/api/v1/reports/balances` | Saldo neto por tipo de cuenta |

`from`/`to`: RFC3339 opcionales. `granularity`: `day` \| `week` \| `month` (default `day`).

## Aislamiento por comercio

Todas las consultas filtran por `tenant_id` (tomado del contexto, inyectado por
el middleware JWT). Las tablas tienen RLS habilitado como defensa en profundidad
para las escrituras de proyección.
