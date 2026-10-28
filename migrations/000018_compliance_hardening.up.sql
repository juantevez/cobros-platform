-- migrations/000018_compliance_hardening.up.sql
-- Compliance & AML (deuda técnica, ver docs/technical/COMPLIANCE.md §10):
--   1. aml_alerts.resolved_by: trazabilidad de quién dispuso la alerta.
--   2. Nuevo tipo de alerta transaction_volume (monto acumulado en ventana).
--   3. Política RLS de aml_alerts tolerante a variable vacía (NULLIF): tras una
--      tx con set_config(..., true) la conexión del pool conserva '' y
--      ''::uuid fallaría con 22P02.
--   4. aml_watchlist.removed_at: baja lógica de entradas (DELETE por API).
--   5. Re-normalización de normalized_name (sin acentos ni puntuación) para
--      alinearla con domain.NormalizeName, e índice GIN para el screening por
--      tokens (string_to_array(...) <@ string_to_array($1, ' ')).
--   6. aml_monitored_payments: proyección propia de pagos capturados
--      (desde payment.captured.v1) para las reglas de ventana. Reemplaza la
--      lectura cruzada de la tabla payments.
--   7. Baja de la semilla de ejemplo de 000014: los nombres ficticios no deben
--      participar del screening en producción. Para desarrollo local:
--      deploy/seed/compliance_watchlist.sql (make seed-dev).

-- 1 ─────────────────────────────────────────────────────────────────────────
ALTER TABLE aml_alerts ADD COLUMN resolved_by TEXT;

-- 2 ─────────────────────────────────────────────────────────────────────────
ALTER TABLE aml_alerts DROP CONSTRAINT aml_alerts_alert_type_check;
ALTER TABLE aml_alerts ADD CONSTRAINT aml_alerts_alert_type_check
    CHECK (alert_type IN (
        'sanctions_match',
        'transaction_threshold',
        'transaction_velocity',
        'transaction_volume'
    ));

-- 3 ─────────────────────────────────────────────────────────────────────────
DROP POLICY aml_alerts_isolation ON aml_alerts;
CREATE POLICY aml_alerts_isolation ON aml_alerts
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

-- 4 ─────────────────────────────────────────────────────────────────────────
ALTER TABLE aml_watchlist ADD COLUMN removed_at TIMESTAMPTZ;

-- 5 ─────────────────────────────────────────────────────────────────────────
-- Réplica SQL de domain.NormalizeName para acentos latinos comunes. Nombres
-- con otros diacríticos conviene re-cargarlos por API (normaliza en Go).
UPDATE aml_watchlist
SET normalized_name = btrim(regexp_replace(
        lower(translate(full_name,
            'ÁÀÄÂÃÅáàäâãåÉÈËÊéèëêÍÌÏÎíìïîÓÒÖÔÕóòöôõÚÙÜÛúùüûÑñÇç',
            'AAAAAAaaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuNnCc')),
        '[^[:alnum:]]+', ' ', 'g'));

CREATE INDEX idx_aml_watchlist_tokens ON aml_watchlist
    USING GIN (string_to_array(normalized_name, ' '))
    WHERE removed_at IS NULL;

-- 6 ─────────────────────────────────────────────────────────────────────────
CREATE TABLE aml_monitored_payments (
    payment_id  TEXT        PRIMARY KEY,   -- idempotencia ante re-entregas
    tenant_id   UUID        NOT NULL,
    amount      BIGINT      NOT NULL,
    currency    TEXT        NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_aml_monitored_payments_window
    ON aml_monitored_payments (tenant_id, captured_at);

ALTER TABLE aml_monitored_payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY aml_monitored_payments_isolation ON aml_monitored_payments
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

COMMENT ON TABLE aml_monitored_payments IS
    'Proyección de payment.captured.v1 para reglas AML de ventana (velocity/volumen).';

-- 7 ─────────────────────────────────────────────────────────────────────────
UPDATE aml_watchlist SET removed_at = now()
WHERE removed_at IS NULL
  AND (full_name, list_type, country, source) IN (
        ('Vladimir Petrov',       'sanctions', 'RU', 'OFAC'),
        ('Ivan Sokolov',          'sanctions', 'RU', 'EU'),
        ('Kim Jong Trading Co',   'sanctions', 'KP', 'UN'),
        ('Global Shell Holdings', 'pep',       'PA', 'EU'));
