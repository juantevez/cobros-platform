# Compliance & AML

Screening contra listas de vigilancia (sanciones/PEP) y monitoreo transaccional.
Es un contexto **write-side** (agregado `Alert`) alimentado por eventos de otros
contextos. Genera alertas para **revisión manual**: no toma acción automática
sobre el comercio (sin auto-suspensión).

## Flujo

```
NATS JetStream
  ├── ONBOARDING (onboarding.application.submitted.v1) ─┐
  └── PAYMENT    (payment.captured.v1)                  │
                                                        ▼
             inbound/nats/EventConsumer  (cmd/worker)
                       │                         │
              ScreenApplication          MonitorTransaction
              (watchlist match)          (umbral + velocity)
                       └────────────┬────────────┘
                                    ▼
                        Alert (open) + AlertRaisedEvent
                                    ▼
                 RunInTx { AlertRepository.Save + Outbox.Publish }
                                    ▼
                inbound/http (cmd/api): listar / ver / resolver
```

## Reglas del MVP

| Regla | Disparador | Alerta |
|---|---|---|
| Screening | `legal_name` ∈ `aml_watchlist` (containment normalizado) | `sanctions_match` |
| Umbral | `amount ≥ ThresholdAmount` (default $10.000) | `transaction_threshold` |
| Velocity | ≥ `VelocityCount` pagos capturados en `VelocityWindow` (default 10 / 10min) | `transaction_velocity` |

Los umbrales viven en `application.MonitoringRules` (`DefaultMonitoringRules()`),
inyectados en `MonitorTransactionUseCase` desde `cmd/worker`.

## Idempotencia

La unicidad `(tenant_id, alert_type, subject)` en `aml_alerts` hace idempotente
la generación: una re-entrega de JetStream que intente crear la misma alerta
choca con la constraint; el repo devuelve `ErrDuplicateAlert` y el caso de uso
lo trata como no-op (sin publicar de nuevo).

## Watchlist

`aml_watchlist` es una lista **global** (no multi-tenant, sin RLS): se hace
screening de todos los comercios contra ella. Se siembra por migración y se
gestiona por `GET/POST /api/v1/compliance/watchlist`. El match usa containment
sobre el nombre normalizado (`NormalizeName`: minúsculas + espacios colapsados).

## Velocity: lectura cruzada

La regla de velocity cuenta pagos capturados leyendo la tabla `payments`
directamente (`TransactionReader`, misma BD) — patrón de lectura cruzada ya
usado por reconciliation/notification.

## Enforcement

Pasivo por diseño: se **registra** la alerta para que un analista la disponga
(`cleared` = falso positivo, `confirmed` = verdadero positivo). El evento
`compliance.alert.raised.v1` queda disponible en el stream `COMPLIANCE` para
que, a futuro, otros contextos reaccionen (p. ej. auth suspendiendo el tenant).
