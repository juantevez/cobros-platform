-- migrations/000034_reporting_rls_nullif.down.sql
DROP POLICY report_payment_fact_isolation ON report_payment_fact;
CREATE POLICY report_payment_fact_isolation ON report_payment_fact
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY report_ledger_movement_isolation ON report_ledger_movement;
CREATE POLICY report_ledger_movement_isolation ON report_ledger_movement
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
