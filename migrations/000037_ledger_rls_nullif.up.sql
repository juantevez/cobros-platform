-- migrations/000037_ledger_rls_nullif.up.sql
-- RLS del Ledger como el resto de las tablas con datos por tenant.
--   - Aislamiento por tenant, tolerante a variable vacía (NULLIF: tras una tx
--     con set_config(..., is_local=true) la conexión conserva '' y ''::uuid
--     falla con 22P02).
--   - Operador de plataforma: lectura cross-tenant (conciliación global). La
--     app fija app.platform_operator='on' solo detrás de un guard de rol.
-- Los repositorios y consumers fijan app.current_tenant en cada operación.
-- account_balances no tiene tenant_id: se accede siempre por una cuenta ya
-- validada o con join a ledger_accounts.

DROP POLICY ledger_accounts_tenant_isolation ON ledger_accounts;
CREATE POLICY ledger_accounts_tenant_isolation ON ledger_accounts
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY ledger_accounts_platform_operator ON ledger_accounts
    USING (current_setting('app.platform_operator', true) = 'on');

DROP POLICY journal_entries_tenant_isolation ON journal_entries;
CREATE POLICY journal_entries_tenant_isolation ON journal_entries
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY journal_entries_platform_operator ON journal_entries
    USING (current_setting('app.platform_operator', true) = 'on');

DROP POLICY postings_tenant_isolation ON postings;
CREATE POLICY postings_tenant_isolation ON postings
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY postings_platform_operator ON postings
    USING (current_setting('app.platform_operator', true) = 'on');
