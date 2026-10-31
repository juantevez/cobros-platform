-- migrations/000041_webhook_rls.down.sql
DROP POLICY IF EXISTS webhook_delivery_attempts_platform_operator ON webhook_delivery_attempts;
DROP POLICY IF EXISTS webhook_delivery_attempts_tenant_isolation ON webhook_delivery_attempts;
ALTER TABLE webhook_delivery_attempts DISABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_delivery_attempts DROP CONSTRAINT IF EXISTS webhook_delivery_attempts_delivery_tenant_fkey;
ALTER TABLE webhook_delivery_attempts
    ADD CONSTRAINT webhook_delivery_attempts_delivery_id_fkey
    FOREIGN KEY (delivery_id) REFERENCES webhook_deliveries (id);
DROP INDEX IF EXISTS uq_webhook_deliveries_id_tenant;
ALTER TABLE webhook_delivery_attempts DROP COLUMN IF EXISTS tenant_id;

DROP POLICY IF EXISTS webhook_deliveries_platform_operator ON webhook_deliveries;
DROP POLICY IF EXISTS webhook_deliveries_tenant_isolation ON webhook_deliveries;
CREATE POLICY webhook_deliveries_isolation ON webhook_deliveries
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY IF EXISTS webhook_endpoints_platform_operator ON webhook_endpoints;
DROP POLICY IF EXISTS webhook_endpoints_tenant_isolation ON webhook_endpoints;
CREATE POLICY webhook_endpoints_isolation ON webhook_endpoints
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
