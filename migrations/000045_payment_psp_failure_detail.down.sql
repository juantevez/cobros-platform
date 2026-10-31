-- migrations/000045_payment_psp_failure_detail.down.sql
ALTER TABLE payments DROP COLUMN IF EXISTS psp_failure_detail;
