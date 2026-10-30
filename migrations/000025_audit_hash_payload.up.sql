-- migrations/000025_audit_hash_payload.up.sql
-- El hash de audit_log cubre todos los campos de la entrada (payload canónico
-- JSON, ver domain.AuditLogEntry.canonicalPayload). Solo documentación.
COMMENT ON COLUMN audit_log.hash IS
    'SHA-256 del array JSON [hex(prev_hash), tenant_id, actor, action, resource_type, resource_id, created_at RFC3339Nano UTC (µs), correlation_id, metadata con claves ordenadas].';
