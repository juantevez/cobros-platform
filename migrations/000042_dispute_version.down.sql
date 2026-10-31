-- migrations/000042_dispute_version.down.sql
ALTER TABLE disputes DROP COLUMN IF EXISTS version;
