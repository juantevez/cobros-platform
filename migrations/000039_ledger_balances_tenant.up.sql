-- migrations/000039_ledger_balances_tenant.up.sql
-- account_balances pasa a tener tenant_id y RLS, como el resto del Ledger.
-- Antes el aislamiento dependía de que cada query hiciera join con
-- ledger_accounts o validara la cuenta antes.

ALTER TABLE account_balances ADD COLUMN tenant_id UUID;

UPDATE account_balances ab
SET tenant_id = la.tenant_id
FROM ledger_accounts la
WHERE la.id = ab.account_id;

ALTER TABLE account_balances ALTER COLUMN tenant_id SET NOT NULL;

-- El saldo es del mismo tenant que su cuenta: FK compuesta.
CREATE UNIQUE INDEX uq_ledger_accounts_id_tenant ON ledger_accounts (id, tenant_id);
ALTER TABLE account_balances DROP CONSTRAINT account_balances_account_id_fkey;
ALTER TABLE account_balances
    ADD CONSTRAINT account_balances_account_tenant_fkey
    FOREIGN KEY (account_id, tenant_id) REFERENCES ledger_accounts (id, tenant_id);

ALTER TABLE account_balances ENABLE ROW LEVEL SECURITY;
CREATE POLICY account_balances_tenant_isolation ON account_balances
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY account_balances_platform_operator ON account_balances
    USING (current_setting('app.platform_operator', true) = 'on');
