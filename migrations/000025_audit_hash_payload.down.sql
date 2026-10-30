-- migrations/000025_audit_hash_payload.down.sql
COMMENT ON COLUMN audit_log.hash IS
    'SHA-256(hex(prev_hash)|tenant_id|actor|action|resource_type|resource_id|created_at).';
