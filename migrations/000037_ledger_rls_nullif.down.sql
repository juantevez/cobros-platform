-- migrations/000037_ledger_rls_nullif.down.sql
DROP POLICY postings_platform_operator ON postings;
DROP POLICY postings_tenant_isolation ON postings;
CREATE POLICY postings_tenant_isolation ON postings
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY journal_entries_platform_operator ON journal_entries;
DROP POLICY journal_entries_tenant_isolation ON journal_entries;
CREATE POLICY journal_entries_tenant_isolation ON journal_entries
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY ledger_accounts_platform_operator ON ledger_accounts;
DROP POLICY ledger_accounts_tenant_isolation ON ledger_accounts;
CREATE POLICY ledger_accounts_tenant_isolation ON ledger_accounts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
