-- migrations/000044_payment_refund_reference.down.sql
ALTER TABLE payments
    DROP COLUMN IF EXISTS refunded_at,
    DROP COLUMN IF EXISTS refund_psp_reference;
