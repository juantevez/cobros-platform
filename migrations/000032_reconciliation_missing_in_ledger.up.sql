-- migrations/000032_reconciliation_missing_in_ledger.up.sql
-- Nuevo tipo de discrepancia de la conciliación interna: un pago capturado
-- sin su asiento en el Ledger. ledger_imbalance pasa a ser por asiento
-- (record_id = entry_id) en lugar de un único total "global".

ALTER TABLE reconciliation_discrepancies DROP CONSTRAINT reconciliation_discrepancies_type_check;
ALTER TABLE reconciliation_discrepancies ADD CONSTRAINT reconciliation_discrepancies_type_check
    CHECK (type IN (
        'missing_in_psp', 'missing_in_system',
        'amount_mismatch', 'status_mismatch',
        'ledger_imbalance', 'duplicate_reference',
        'missing_in_ledger'
    ));
