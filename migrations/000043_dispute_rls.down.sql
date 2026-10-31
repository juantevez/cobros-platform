-- migrations/000043_dispute_rls.down.sql
DROP POLICY IF EXISTS payments_platform_operator ON payments;
DROP POLICY IF EXISTS payments_tenant_isolation ON payments;
CREATE POLICY payments_tenant_isolation ON payments
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY IF EXISTS dispute_evidence_platform_operator ON dispute_evidence;
DROP POLICY IF EXISTS dispute_evidence_tenant_isolation ON dispute_evidence;
ALTER TABLE dispute_evidence DISABLE ROW LEVEL SECURITY;
ALTER TABLE dispute_evidence DROP CONSTRAINT IF EXISTS dispute_evidence_dispute_tenant_fkey;
ALTER TABLE dispute_evidence
    ADD CONSTRAINT dispute_evidence_dispute_id_fkey
    FOREIGN KEY (dispute_id) REFERENCES disputes (id);
DROP INDEX IF EXISTS uq_disputes_id_tenant;
ALTER TABLE dispute_evidence DROP COLUMN IF EXISTS tenant_id;

DROP POLICY IF EXISTS disputes_platform_operator ON disputes;
DROP POLICY IF EXISTS disputes_tenant_isolation ON disputes;
CREATE POLICY disputes_tenant_isolation ON disputes
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
