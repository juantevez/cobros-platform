-- migrations/000034_reporting_rls_nullif.up.sql
-- Políticas RLS de Reporting tolerantes a variable vacía: tras una tx que
-- usó set_config(..., is_local=true), la conexión del pool conserva
-- app.current_tenant = '' (no NULL) y ''::uuid falla con 22P02. NULLIF la
-- convierte en NULL → la política evalúa false (0 filas, sin error). Los
-- repositorios fijan la variable en cada operación (RunWithAppTenant).

DROP POLICY report_payment_fact_isolation ON report_payment_fact;
CREATE POLICY report_payment_fact_isolation ON report_payment_fact
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY report_ledger_movement_isolation ON report_ledger_movement;
CREATE POLICY report_ledger_movement_isolation ON report_ledger_movement
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
