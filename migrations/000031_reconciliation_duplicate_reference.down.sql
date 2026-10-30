-- migrations/000031_reconciliation_duplicate_reference.down.sql
DELETE FROM reconciliation_discrepancies WHERE type = 'duplicate_reference';

ALTER TABLE reconciliation_discrepancies DROP CONSTRAINT reconciliation_discrepancies_type_check;
ALTER TABLE reconciliation_discrepancies ADD CONSTRAINT reconciliation_discrepancies_type_check
    CHECK (type IN (
        'missing_in_psp', 'missing_in_system',
        'amount_mismatch', 'status_mismatch',
        'ledger_imbalance'
    ));
