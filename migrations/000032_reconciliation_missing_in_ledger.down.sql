-- migrations/000032_reconciliation_missing_in_ledger.down.sql
DELETE FROM reconciliation_discrepancies WHERE type = 'missing_in_ledger';

ALTER TABLE reconciliation_discrepancies DROP CONSTRAINT reconciliation_discrepancies_type_check;
ALTER TABLE reconciliation_discrepancies ADD CONSTRAINT reconciliation_discrepancies_type_check
    CHECK (type IN (
        'missing_in_psp', 'missing_in_system',
        'amount_mismatch', 'status_mismatch',
        'ledger_imbalance', 'duplicate_reference'
    ));
