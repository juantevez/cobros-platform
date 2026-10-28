-- migrations/000018_compliance_hardening.down.sql
-- La re-normalización de normalized_name no se revierte (es un superset
-- compatible del formato anterior para nombres sin acentos ni puntuación).

DROP TABLE IF EXISTS aml_monitored_payments;

DROP INDEX IF EXISTS idx_aml_watchlist_tokens;
-- Al eliminar removed_at, las entradas dadas de baja (incluida la semilla de
-- ejemplo) vuelven a quedar activas.
ALTER TABLE aml_watchlist DROP COLUMN IF EXISTS removed_at;

DROP POLICY aml_alerts_isolation ON aml_alerts;
CREATE POLICY aml_alerts_isolation ON aml_alerts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DELETE FROM aml_alerts WHERE alert_type = 'transaction_volume';
ALTER TABLE aml_alerts DROP CONSTRAINT aml_alerts_alert_type_check;
ALTER TABLE aml_alerts ADD CONSTRAINT aml_alerts_alert_type_check
    CHECK (alert_type IN (
        'sanctions_match',
        'transaction_threshold',
        'transaction_velocity'
    ));

ALTER TABLE aml_alerts DROP COLUMN IF EXISTS resolved_by;
