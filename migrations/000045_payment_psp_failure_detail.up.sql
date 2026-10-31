-- migrations/000045_payment_psp_failure_detail.up.sql
-- El detalle crudo del error del PSP, solo para diagnóstico interno.
-- failure_reason pasa a guardar un motivo genérico: se expone en la API, en
-- payment.failed.v1 y, por ende, en el email y el webhook al comercio.
ALTER TABLE payments ADD COLUMN psp_failure_detail TEXT;

COMMENT ON COLUMN payments.psp_failure_detail IS
    'Error crudo del PSP al rechazar. Interno: no se expone al comercio.';
