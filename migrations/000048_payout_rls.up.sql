-- migrations/000048_payout_rls.up.sql
-- RLS efectiva en payouts, como en el resto del sistema.
--   - Aislamiento por tenant tolerante a variable vacía (NULLIF: tras una tx
--     con set_config(..., is_local=true) la conexión conserva '' y ''::uuid
--     falla con 22P02).
--   - Operador de plataforma solo de lectura: el recuperador lista los payouts
--     unknown o trabados de todos los tenants y escribe cada uno con su tenant.
DROP POLICY payouts_tenant_isolation ON payouts;
CREATE POLICY payouts_tenant_isolation ON payouts
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY payouts_platform_operator ON payouts FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');
