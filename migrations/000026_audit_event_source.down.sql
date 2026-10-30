-- migrations/000026_audit_event_source.down.sql
DROP INDEX IF EXISTS uq_audit_log_event_id;
ALTER TABLE audit_log DROP COLUMN IF EXISTS occurred_at, DROP COLUMN IF EXISTS event_id;
COMMENT ON COLUMN audit_log.hash IS
    'SHA-256 del array JSON [hex(prev_hash), tenant_id, actor, action, resource_type, resource_id, created_at RFC3339Nano UTC (µs), correlation_id, metadata con claves ordenadas].';
