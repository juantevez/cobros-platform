-- migrations/000027_audit_append_only.down.sql
DROP TABLE IF EXISTS audit_chain_checkpoints;
DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
DROP TRIGGER IF EXISTS audit_log_append_only ON audit_log;
DROP FUNCTION IF EXISTS audit_reject_mutation();
