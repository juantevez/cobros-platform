-- migrations/000049_payout_bank_failure_detail.down.sql
ALTER TABLE payouts DROP COLUMN IF EXISTS bank_failure_detail;
