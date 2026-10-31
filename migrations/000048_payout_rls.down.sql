-- migrations/000048_payout_rls.down.sql
DROP POLICY IF EXISTS payouts_platform_operator ON payouts;
DROP POLICY IF EXISTS payouts_tenant_isolation ON payouts;
CREATE POLICY payouts_tenant_isolation ON payouts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
