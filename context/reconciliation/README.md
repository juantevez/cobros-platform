# context/reconciliation — Reconciliación

Detecta discrepancias entre los registros internos de la plataforma y los informes externos del PSP o banco. Garantiza que el dinero registrado en el sistema coincide exactamente con el dinero movido en el mundo real.

---

## Responsabilidades

- Comparar pagos del sistema contra el informe CSV del PSP
- Verificar la coherencia matemática interna del Ledger
- Registrar cada discrepancia encontrada con los valores de ambos lados
- Permitir al operador resolver o ignorar discrepancias con trazabilidad

---

## Tipos de reconciliación

### `payment` — Sistema vs PSP

Compara la tabla `payments` contra un CSV exportado del PSP para un período.

Detecta:

| Tipo | Descripción | Causa probable |
|---|---|---|
| `missing_in_psp` | Pago en sistema, no en PSP | PSP perdió el registro; error de red |
| `missing_in_system` | PSP reporta transacción no conocida | Pago procesado pero no persistido |
| `amount_mismatch` | Montos distintos | Fee incorrecto; redondeo; fraude |
| `status_mismatch` | Estados críticos inconsistentes | Bug de integración; race condition |

### `internal_ledger` — Coherencia del Ledger

Verifica que `SUM(débitos) == SUM(créditos)` para todos los postings del período. Un imbalance indica un bug en la lógica de asientos o corrupción de datos.

---

## Estructura

```
context/reconciliation/
├── domain/
│   ├── errors.go        # 7 errores de negocio
│   ├── vo.go            # RunID, DiscrepancyID, tipos y estados
│   ├── events.go        # ReconciliationCompletedEvent
│   ├── run.go           # Agregado ReconciliationRun (FSM)
│   └── discrepancy.go   # Entidad Discrepancy con Resolve/Ignore
├── application/
│   ├── ports.go             # RunRepository, DiscrepancyRepository,
│   │                        # PaymentReader, ReportParser, LedgerChecker
│   ├── commands.go          # DTOs + RunView, ReportView, DiscrepancyView
│   ├── start_reconciliation.go
│   ├── process_report.go    # Lógica de comparación + ProcessInternalUseCase
│   └── resolve_discrepancy.go  # + GetReportUseCase + ListRunsUseCase
└── infrastructure/adapters/
    ├── inbound/http/handler.go
    └── outbound/
        ├── postgres/run_repo.go
        ├── postgres/discrepancy_repo.go
        └── readers/
            ├── payment_reader.go    # Lee de tabla payments
            ├── payment_reader.go    # LedgerChecker: sum postings
            └── csv_parser.go        # Parsea CSV del PSP
```

---

## Flujo: reconciliación de pagos

```
1. POST /reconciliation/runs
   { type: "payment", period_from: "...", period_to: "..." }
   → Crea run en estado "pending"

2. POST /reconciliation/runs/:runID/report
   Content-Type: text/csv
   Body: <archivo CSV del PSP>
   → ProcessReport:
      a. Parsea CSV → []PSPRecord
      b. Lee pagos del sistema → []SystemPayment
      c. Indexa por psp_reference / transaction_id
      d. Compara: missing, amount_mismatch, status_mismatch
      e. Guarda discrepancias
      f. Run → "completed"

3. GET /reconciliation/runs/:runID?status=open
   → Reporte con sumario + discrepancias abiertas

4. POST /reconciliation/discrepancies/:id/resolve
   { action: "resolve", resolved_by: "user@co.com", notes: "Confirmado con PSP" }
   → Discrepancia → "resolved"
```

---

## API

```
POST /api/v1/reconciliation/runs                           Iniciar run
GET  /api/v1/reconciliation/runs?limit=20                  Listar runs
GET  /api/v1/reconciliation/runs/:runID?status=open        Reporte del run
POST /api/v1/reconciliation/runs/:runID/report             Subir CSV del PSP
POST /api/v1/reconciliation/runs/:runID/process-internal   Reconciliación interna
POST /api/v1/reconciliation/discrepancies/:id/resolve      Resolver/ignorar
```

---

## Formato del CSV del PSP

```csv
transaction_id,amount,currency,status,processed_at
PSP-REF-001,10000,ARS,captured,2026-06-01T12:00:00Z
PSP-REF-002,5000,USD,rejected,2026-06-01T12:05:00Z
PSP-REF-003,25000,ARS,captured,2026-06-01T12:10:00Z
```

- `transaction_id` = `psp_reference` almacenado en la tabla `payments`
- `amount` en centavos (unidades mínimas)
- `status`: `captured | rejected | refunded | pending`
- `processed_at`: RFC3339 (opcional)

---

## Dependencias del contexto

```
Produce eventos → stream RECONCILIATION (reconciliation.run.completed.v1)
Consume eventos → ninguno

Lee directamente de → payments (del contexto Payment, misma BD)
                   → postings + journal_entries (del Ledger, misma BD)

Depende de → pkg/postgres
             pkg/outbox (EventPublisher)
```
