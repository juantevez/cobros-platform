# context/audit — Audit & Observability

Mantiene una bitácora **inmutable y tamper-evident** de todas las acciones sensibles del sistema. No produce eventos de dominio propios: solo los consume desde otros contextos vía NATS JetStream y los registra de forma auditada.

---

## Responsabilidades

- Registrar cada acción sensible (aprobación de KYC, asientos contables, cambios de credenciales, etc.)
- Garantizar que el log **no puede modificarse** sin que se detecte mediante encadenamiento de hash SHA-256
- Exponer una API de consulta para el operador de la plataforma
- Proveer un endpoint de **verificación de integridad** de la cadena

---

## Estructura

```
context/audit/
├── domain/
│   ├── errors.go      # ErrLogEntryNotFound, ErrChainBroken
│   ├── vo.go          # Action, ResourceType (constantes auditables)
│   └── log_entry.go   # AuditLogEntry: entidad append-only con hash chain
├── application/
│   ├── ports.go       # AuditLogRepository, HashComputer, Clock
│   ├── commands.go    # RecordActionCmd, VerifyChainQuery, ListLogsQuery, LogEntryView
│   ├── record_action.go   # Registra una acción con hash encadenado
│   ├── verify_chain.go    # Verifica la integridad de la cadena
│   └── list_logs.go       # Consulta entradas del log
└── infrastructure/adapters/
    ├── inbound/
    │   ├── http/
    │   │   └── audit_handler.go   # GET /audit/logs, GET /audit/verify
    │   └── nats/
    │       └── event_consumer.go  # Consume auth.> y ledger.> → RecordAction
    └── outbound/
        ├── postgres/
        │   └── audit_log_repo.go  # Implementación con SELECT FOR UPDATE
        └── crypto/
            └── sha256_hasher.go   # HashComputer: SHA-256
```

---

## El hash chain

Cada entrada del log encadena el hash de la anterior:

```
hash_n = SHA-256( hex(hash_{n-1}) | tenantID | actor | action | resourceType | resourceID | createdAt )
```

El separador `|` y el orden de los campos son fijos e inmutables. Si alguien modifica cualquier campo de la entrada `k`:

1. `hash_k` cambia al recalcularse
2. `prev_hash` de `k+1` ya no coincide con el nuevo `hash_k`
3. `VerifyChain` detecta la manipulación y reporta el ID del primer registro inválido

```
entry_1: prev_hash=nil,        hash=H(nil|...|created_at_1)
entry_2: prev_hash=hash_1,     hash=H(hash_1|...|created_at_2)
entry_3: prev_hash=hash_2,     hash=H(hash_2|...|created_at_3)
             ↑
        Si se modifica entry_2, hash_2 cambia → entry_3 queda inválida
```

### Serialización atómica

`RecordAction` lee el último hash e inserta el nuevo registro dentro de la misma operación, usando `SELECT ... FOR UPDATE`:

```sql
-- FindLast (con FOR UPDATE serializa inserciones concurrentes)
SELECT id, hash FROM audit_log ORDER BY id DESC LIMIT 1 FOR UPDATE;

-- Luego INSERT con el hash calculado en Go
INSERT INTO audit_log (..., prev_hash, hash) VALUES (...);
```

En Fase 1 el consumer es un único goroutine (pull secuencial), por lo que no hay contención. El `FOR UPDATE` protege ante múltiples instancias del worker en producción.

---

## Dominio

### `AuditLogEntry`

La entidad central. Es append-only: una vez persistida, nunca se modifica ni elimina.

```go
type AuditLogEntry struct {
    id            int64          // BIGSERIAL — garantiza orden del chain
    tenantID      string         // vacío para acciones de plataforma
    actor         string         // userID, "system", o "relay"
    action        Action         // ej: "auth.tenant.activated"
    resourceType  ResourceType   // ej: "tenant"
    resourceID    string         // UUID del recurso afectado
    metadata      map[string]string
    correlationID string
    prevHash      []byte         // nil en el primer registro
    hash          []byte         // SHA-256 del payload canónico
    createdAt     time.Time
}
```

**Métodos clave:**
- `VerifyHash(compute)` — recalcula el hash y compara con el almacenado
- `ChainLinksTo(prev)` — verifica que `entry.prevHash == prev.hash`
- `HashHex()` / `PrevHashHex()` — representación hexadecimal para APIs

### Acciones auditadas

```go
// Auth
ActionTenantCreated   = "auth.tenant.created"
ActionTenantActivated = "auth.tenant.activated"
ActionTenantSuspended = "auth.tenant.suspended"
ActionUserRegistered  = "auth.user.registered"
ActionApiKeyIssued    = "auth.apikey.issued"
ActionApiKeyRevoked   = "auth.apikey.revoked"
ActionRoleAssigned    = "auth.role.assigned"

// Ledger
ActionAccountCreated  = "ledger.account.created"
ActionEntryPosted     = "ledger.entry.posted"
ActionEntryReversed   = "ledger.entry.reversed"
```

---

## Casos de uso

### `RecordAction`

Registra una acción sensible en el log encadenado.

```
1. Buscar el último registro (FindLast con FOR UPDATE)
2. Extraer su hash como prev_hash
3. Construir AuditLogEntry con hash = SHA-256(prev_hash + payload)
4. Persistir
```

### `VerifyChain`

Recorre la cadena y detecta manipulaciones.

```
Para cada entrada (en orden ascendente):
  1. Recalcular hash con el payload almacenado
  2. Comparar con el hash almacenado → si difiere: manipulación detectada
  3. Verificar que prev_hash == hash de la entrada anterior → si difiere: cadena rota

Retorna: { valid: bool, entries_checked: int, first_invalid_id: int64 }
```

### `ListLogs`

Consulta entradas del log con filtros opcionales. Límite: 200 entradas por request.

---

## Consumer de NATS

El contexto Audit suscribe dos consumers durables pull-based:

| Consumer | Stream | Filter | Lo que audita |
|---|---|---|---|
| `audit-auth-consumer` | AUTH | `auth.>` | Todos los eventos de Auth |
| `audit-ledger-consumer` | LEDGER | `ledger.>` | Todos los eventos de Ledger |

```
Evento recibido de NATS
  → mapEvent(msg): traduce subject → RecordActionCmd
  → RecordAction.Execute()
  → Si handler retorna error → Nak → reintento con backoff
  → Si evento desconocido → log warn + Ack (no bloquea la cola)
```

El Audit **nunca bloquea** el procesamiento de pagos: si falla al registrar un evento, reintenta por separado sin afectar el flujo principal.

---

## API

Todos los endpoints requieren JWT con rol `platform_support` o `admin`.

```
GET /api/v1/audit/logs
    ?limit=50           Últimas N entradas (máx 200)
    ?tenant_id=<uuid>   Filtrar por comercio (opcional)

GET /api/v1/audit/verify
    ?from_id=0          Verificar desde este ID (0 = desde el inicio)
    ?limit=500          Cuántas entradas verificar

```

### Respuesta de `/audit/logs`

```json
{
  "entries": [
    {
      "id": 42,
      "tenant_id": "...",
      "actor": "system",
      "action": "auth.tenant.activated",
      "resource_type": "tenant",
      "resource_id": "...",
      "prev_hash": "a3f1...",
      "hash": "b9c2...",
      "created_at": "2026-06-01T12:00:00Z"
    }
  ],
  "count": 1
}
```

### Respuesta de `/audit/verify` — cadena íntegra

```json
{ "valid": true, "entries_checked": 1500 }
```

### Respuesta de `/audit/verify` — manipulación detectada

```json
{
  "valid": false,
  "entries_checked": 847,
  "first_invalid_id": 312,
  "first_invalid_msg": "hash mismatch at entry id=312"
}
```

---

## Tabla en PostgreSQL

```sql
CREATE TABLE audit_log (
    id             BIGSERIAL   PRIMARY KEY,   -- orden secuencial del chain
    tenant_id      UUID,                      -- NULL para acciones de plataforma
    actor          TEXT        NOT NULL,
    action         TEXT        NOT NULL,
    resource_type  TEXT        NOT NULL,
    resource_id    TEXT,
    metadata       JSONB       NOT NULL DEFAULT '{}',
    correlation_id TEXT,
    prev_hash      BYTEA,                     -- NULL solo en el primer registro
    hash           BYTEA       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL
);
```

**Sin RLS**: el log es global; el operador necesita ver todos los tenants. El aislamiento se aplica en la query (`WHERE tenant_id = ?`).

**Append-only**: sin `UPDATE` ni `DELETE`. Las correcciones no existen — si hay un error, se registra una nueva entrada que lo describe.

---

## Dependencias del contexto

```
Produce eventos → ninguno (contexto receptor puro)
Consume eventos → stream AUTH   (auth.>)
                → stream LEDGER (ledger.>)

Depende de → pkg/postgres  (pool)
             pkg/eventbus  (Consumer)
```

---

## Consideraciones de retención

Los registros de auditoría deben retenerse según los requisitos regulatorios aplicables (típicamente 5-10 años en contextos financieros). En producción, configurar una política de retención en la tabla o archivar entradas antiguas a almacenamiento frío manteniendo los hashes para verificación posterior.

Para auditorías externas, exportar el rango de IDs relevante y ejecutar `VerifyChain` desde ese rango para demostrar integridad.