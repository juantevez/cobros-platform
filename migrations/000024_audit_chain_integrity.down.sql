-- migrations/000024_audit_chain_integrity.down.sql
DROP INDEX IF EXISTS uq_audit_log_genesis;
DROP INDEX IF EXISTS uq_audit_log_prev_hash;
