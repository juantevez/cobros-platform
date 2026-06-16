-- migrations/000007_payout_init.down.sql
DROP TABLE IF EXISTS payouts;

-- Revertir el CHECK de ledger_accounts al estado anterior.
ALTER TABLE ledger_accounts
    DROP CONSTRAINT IF EXISTS ledger_accounts_type_check;

ALTER TABLE ledger_accounts
    ADD CONSTRAINT ledger_accounts_type_check
    CHECK (type IN (
        'merchant_balance', 'platform_fees',
        'reserve', 'in_transit', 'dispute_hold'
    ));
