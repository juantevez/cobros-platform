-- migrations/000044_payment_refund_reference.up.sql
-- La referencia del reembolso en el PSP y su fecha. Antes solo viajaban en el
-- evento y en la respuesta: no había forma de conciliar ni consultar un
-- reembolso desde la base.
ALTER TABLE payments
    ADD COLUMN refund_psp_reference TEXT,
    ADD COLUMN refunded_at          TIMESTAMPTZ;
