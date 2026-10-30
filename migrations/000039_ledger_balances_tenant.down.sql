-- migrations/000039_ledger_balances_tenant.down.sql
DROP POLICY account_balances_platform_operator ON account_balances;
DROP POLICY account_balances_tenant_isolation ON account_balances;
ALTER TABLE account_balances DISABLE ROW LEVEL SECURITY;

ALTER TABLE account_balances DROP CONSTRAINT account_balances_account_tenant_fkey;
ALTER TABLE account_balances
    ADD CONSTRAINT account_balances_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES ledger_accounts (id);
DROP INDEX uq_ledger_accounts_id_tenant;

ALTER TABLE account_balances DROP COLUMN tenant_id;
