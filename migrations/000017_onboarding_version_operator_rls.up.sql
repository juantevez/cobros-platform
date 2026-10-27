-- migrations/000017_onboarding_version_operator_rls.up.sql
-- Onboarding:
--   1. Optimistic locking: columna version en onboarding_applications.
--   2. Política RLS de operador de plataforma: la revisión de KYC opera sobre
--      solicitudes de OTROS tenants. Las políticas permisivas se combinan con
--      OR, así que el aislamiento por tenant sigue vigente para el resto.
--      La app fija app.platform_operator='on' solo en rutas con guard de rol
--      platform_support (pkg/postgres.WithPlatformOperator).
--   3. Políticas de tenant tolerantes a variable vacía: tras una tx que usó
--      set_config(..., is_local=true), la conexión del pool conserva la
--      variable como '' (no NULL) y ''::uuid falla con 22P02. NULLIF la
--      convierte en NULL → la política evalúa false (0 filas, sin error).

ALTER TABLE onboarding_applications
    ADD COLUMN version BIGINT NOT NULL DEFAULT 0;

CREATE POLICY onboarding_platform_operator ON onboarding_applications
    USING (current_setting('app.platform_operator', true) = 'on');

CREATE POLICY onboarding_docs_platform_operator ON onboarding_documents
    USING (current_setting('app.platform_operator', true) = 'on');

CREATE POLICY onboarding_persons_platform_operator ON onboarding_persons
    USING (current_setting('app.platform_operator', true) = 'on');

CREATE POLICY onboarding_bank_platform_operator ON onboarding_bank_accounts
    USING (current_setting('app.platform_operator', true) = 'on');

DROP POLICY onboarding_tenant_isolation ON onboarding_applications;
CREATE POLICY onboarding_tenant_isolation ON onboarding_applications
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY onboarding_docs_isolation ON onboarding_documents;
CREATE POLICY onboarding_docs_isolation ON onboarding_documents
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY onboarding_persons_isolation ON onboarding_persons;
CREATE POLICY onboarding_persons_isolation ON onboarding_persons
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY onboarding_bank_isolation ON onboarding_bank_accounts;
CREATE POLICY onboarding_bank_isolation ON onboarding_bank_accounts
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
