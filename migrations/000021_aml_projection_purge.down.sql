-- migrations/000021_aml_projection_purge.down.sql
DROP POLICY IF EXISTS aml_monitored_payments_platform_operator ON aml_monitored_payments;
DROP INDEX IF EXISTS idx_aml_monitored_payments_captured_at;
