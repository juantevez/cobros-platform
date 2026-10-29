-- migrations/000021_aml_projection_purge.up.sql
-- Purga de la proyección aml_monitored_payments (ProjectionPurger, cmd/worker).
--   1. Índice por captured_at: el DELETE de vencidos no filtra por tenant.
--   2. Política RLS de operador: el purgador corre sin tenant en el contexto
--      (con un rol sin BYPASSRLS la política por tenant no vería filas y el
--      DELETE borraría 0 filas sin error).

CREATE INDEX idx_aml_monitored_payments_captured_at
    ON aml_monitored_payments (captured_at);

CREATE POLICY aml_monitored_payments_platform_operator ON aml_monitored_payments
    USING (current_setting('app.platform_operator', true) = 'on');
