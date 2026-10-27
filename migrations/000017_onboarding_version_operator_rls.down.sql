-- migrations/000017_onboarding_version_operator_rls.down.sql

-- Políticas de tenant originales (000005).
DROP POLICY onboarding_tenant_isolation ON onboarding_applications;
CREATE POLICY onboarding_tenant_isolation ON onboarding_applications
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
DROP POLICY onboarding_docs_isolation ON onboarding_documents;
CREATE POLICY onboarding_docs_isolation ON onboarding_documents
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
DROP POLICY onboarding_persons_isolation ON onboarding_persons;
CREATE POLICY onboarding_persons_isolation ON onboarding_persons
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
DROP POLICY onboarding_bank_isolation ON onboarding_bank_accounts;
CREATE POLICY onboarding_bank_isolation ON onboarding_bank_accounts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY IF EXISTS onboarding_bank_platform_operator ON onboarding_bank_accounts;
DROP POLICY IF EXISTS onboarding_persons_platform_operator ON onboarding_persons;
DROP POLICY IF EXISTS onboarding_docs_platform_operator ON onboarding_documents;
DROP POLICY IF EXISTS onboarding_platform_operator ON onboarding_applications;

ALTER TABLE onboarding_applications DROP COLUMN IF EXISTS version;
