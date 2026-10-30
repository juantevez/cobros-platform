-- migrations/000031_reconciliation_duplicate_reference.up.sql
-- Nuevo tipo de discrepancia: la misma referencia aparece más de una vez en
-- el informe del PSP o en payments, y el matching no puede elegir con qué
-- registro comparar.

ALTER TABLE reconciliation_discrepancies DROP CONSTRAINT reconciliation_discrepancies_type_check;
ALTER TABLE reconciliation_discrepancies ADD CONSTRAINT reconciliation_discrepancies_type_check
    CHECK (type IN (
        'missing_in_psp', 'missing_in_system',
        'amount_mismatch', 'status_mismatch',
        'ledger_imbalance', 'duplicate_reference'
    ));
