-- migrations/000049_payout_bank_failure_detail.up.sql
-- El error crudo del banco, solo para diagnóstico interno. failure_reason
-- pasa a guardar un motivo genérico: se expone en la API, en
-- payout.failed.v1 / payout.unknown.v1 y, por ende, en el email y el webhook.
ALTER TABLE payouts ADD COLUMN bank_failure_detail TEXT;

COMMENT ON COLUMN payouts.bank_failure_detail IS
    'Error crudo del banco al rechazar o no confirmar. Interno: no se expone al comercio.';
