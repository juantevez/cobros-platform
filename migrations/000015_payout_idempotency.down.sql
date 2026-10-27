-- migrations/000015_payout_idempotency.down.sql

DROP INDEX IF EXISTS uq_payouts_tenant_idempotency;

ALTER TABLE payouts DROP COLUMN IF EXISTS idempotency_key;

ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_status_check;
ALTER TABLE payouts
    ADD CONSTRAINT payouts_status_check
    CHECK (status IN ('initiated','processing','confirmed','failed'));
