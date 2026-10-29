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
| Screening | Todos los tokens de una entrada vigente de `aml_watchlist` presentes en el `legal_name` normalizado | `sanctions_match` |
| Umbral | `amount ≥ ThresholdAmount` (default $10.000) | `transaction_threshold` |
| Velocity | ≥ `VelocityCount` pagos capturados en `VelocityWindow` (default 10 / 10min) — ventana deslizante con cooldown (una alerta por pico) | `transaction_velocity` |
| Volumen | ≥ `VolumeAmount` acumulado por moneda en `VelocityWindow` (default $50.000) — ventana deslizante con cooldown por moneda | `transaction_volume` |

Los umbrales viven en `application.MonitoringRules` (`DefaultMonitoringRules()`),
inyectados en `MonitorTransactionUseCase` desde `cmd/worker`.

## Idempotencia

La unicidad `(tenant_id, alert_type, subject)` en `aml_alerts` hace idempotente
la generación: una re-entrega de JetStream que intente crear la misma alerta
choca con la constraint; el repo devuelve `ErrDuplicateAlert` y el caso de uso
lo trata como no-op (sin publicar de nuevo).

## Watchlist

`aml_watchlist` es una lista **global** (no multi-tenant, sin RLS): se hace
screening de todos los comercios contra ella. La gestionan `compliance_officer`
y `platform_support` por `GET/POST/DELETE /api/v1/compliance/watchlist` (el
DELETE es baja lógica, `removed_at`) y, para listas completas,
`POST /api/v1/compliance/watchlist/import?source=OFAC` con un CSV
(`external_ref,full_name,list_type,country`) que sincroniza la fuente
(alta/cambio/baja, atómico, con `dry_run`). La semilla de ejemplo es solo para desarrollo
(`make seed-dev`). El match es por tokens completos sobre el nombre normalizado
(`NormalizeName`: sin acentos ni puntuación, minúsculas, espacios colapsados) y
se puntúa con `MatchScore` (exacto 100, contiguo 90, reordenado 80).

## Reglas de ventana: proyección propia

Velocity y volumen cuentan sobre `aml_monitored_payments`, una proyección que
`MonitorTransaction` alimenta desde `payment.captured.v1` (`TransactionLog`,
idempotente por `payment_id`). Compliance no lee el esquema de Payment.

## Revisión (la plataforma revisa a los comercios)

Las alertas las revisa el equipo de cumplimiento de la plataforma (rol
`compliance_officer`, no asignable por un tenant), sobre alertas de cualquier
comercio (RLS de operador). El comercio nunca ve ni dispone sus alertas.
Four-eyes: las alertas `high` o `sanctions_match` quedan
`pending_second_approval` tras la primera disposición y las cierra (o rechaza)
un segundo analista distinto.

## Enforcement: hold reversible

Un match exacto (score 100) contra una lista de sanciones emite
`compliance.sanction.matched.v1`; Auth pone al comercio en hold
(`tenant_compliance_holds`) y los gates de Payment y Payout bloquean cobros y
desembolsos. Al cerrar la alerta: `cleared` → `sanction.cleared.v1` levanta el
hold; `confirmed` → `sanction.confirmed.v1` suspende al tenant con un motivo
genérico (anti tipping-off). Ver [COMPLIANCE.md](../../docs/technical/COMPLIANCE.md) §7.3.
