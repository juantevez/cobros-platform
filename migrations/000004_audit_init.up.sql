-- migrations/000004_audit_init.up.sql
-- Log de auditoría inmutable con hash chain tamper-evident.
--
-- Principios:
--   - Append-only: sin UPDATE ni DELETE.
--   - Hash chain: cada registro referencia el hash del anterior.
--   - Sin RLS: el audit es un log global; el aislamiento se aplica en queries.
--   - BIGSERIAL: ID secuencial para garantizar el orden del chain.

CREATE TABLE audit_log (
    id             BIGSERIAL   PRIMARY KEY,
    tenant_id      UUID,                             -- NULL para acciones de plataforma
    actor          TEXT        NOT NULL,             -- user_id o "system"
    action         TEXT        NOT NULL,             -- ej: "auth.tenant.activated"
    resource_type  TEXT        NOT NULL,             -- ej: "tenant"
    resource_id    TEXT,                             -- UUID del recurso
    metadata       JSONB       NOT NULL DEFAULT '{}',
    correlation_id TEXT,
    -- Hash chain: hash_n = SHA-256(hex(hash_{n-1}) | tenant | actor | action | resource_type | resource_id | created_at)
    prev_hash      BYTEA,                            -- NULL para el primer registro
    hash           BYTEA       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Índice para consultas por tenant (más frecuente).
CREATE INDEX idx_audit_log_tenant_created
    ON audit_log (tenant_id, created_at DESC)
    WHERE tenant_id IS NOT NULL;

-- Índice para recorrer el chain en orden ascendente (VerifyChain).
CREATE INDEX idx_audit_log_id_asc ON audit_log (id ASC);

-- Proteger la tabla: solo INSERT permitido para el rol de la aplicación.
-- En producción, crear un rol específico y revocar UPDATE/DELETE.
-- ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;  ← no aplica aquí (log global)

COMMENT ON TABLE audit_log IS
    'Bitácora inmutable de acciones sensibles. Hash chain tamper-evident. Append-only.';
COMMENT ON COLUMN audit_log.prev_hash IS
    'Hash SHA-256 del registro anterior. NULL en el primer registro del sistema.';
COMMENT ON COLUMN audit_log.hash IS
    'SHA-256(hex(prev_hash)|tenant_id|actor|action|resource_type|resource_id|created_at).';
