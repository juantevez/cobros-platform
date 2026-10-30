-- migrations/000026_audit_event_source.up.sql
-- Origen de cada entrada de audit_log:
--   event_id    → ID del evento de dominio (Nats-Msg-Id). Idempotencia ante
--                 re-entregas: una entrada por evento.
--   occurred_at → cuándo ocurrió el hecho (header occurred-at del evento).
--                 created_at queda como hora de registro en la bitácora.
-- Ambos entran en el hash (domain.AuditLogEntry.canonicalPayload).

ALTER TABLE audit_log
    ADD COLUMN event_id    TEXT,
    ADD COLUMN occurred_at TIMESTAMPTZ;

UPDATE audit_log SET occurred_at = created_at WHERE occurred_at IS NULL;
ALTER TABLE audit_log ALTER COLUMN occurred_at SET NOT NULL;

CREATE UNIQUE INDEX uq_audit_log_event_id ON audit_log (event_id)
    WHERE event_id IS NOT NULL;

COMMENT ON COLUMN audit_log.hash IS
    'SHA-256 del array JSON [hex(prev_hash), tenant_id, actor, action, resource_type, resource_id, created_at, correlation_id, metadata con claves ordenadas, event_id, occurred_at] (timestamps RFC3339Nano UTC, µs).';
