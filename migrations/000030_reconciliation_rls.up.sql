-- migrations/000030_reconciliation_rls.up.sql
-- RLS de Reconciliation, como el resto de las tablas con datos por tenant.
--   - Aislamiento por tenant, tolerante a variable vacía (NULLIF: tras una tx
--     con set_config(..., is_local=true) la conexión conserva '' y ''::uuid
--     falla con 22P02).
--   - Operador de plataforma: única vía a los runs globales (tenant_id NULL).
--     La app fija app.platform_operator='on' solo detrás de un guard de rol.
-- Los repositorios fijan app.current_tenant en cada operación.

ALTER TABLE reconciliation_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY reconciliation_runs_isolation ON reconciliation_runs
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY reconciliation_runs_platform_operator ON reconciliation_runs
    USING (current_setting('app.platform_operator', true) = 'on');

ALTER TABLE reconciliation_discrepancies ENABLE ROW LEVEL SECURITY;
CREATE POLICY reconciliation_discrepancies_isolation ON reconciliation_discrepancies
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY reconciliation_discrepancies_platform_operator ON reconciliation_discrepancies
    USING (current_setting('app.platform_operator', true) = 'on');
