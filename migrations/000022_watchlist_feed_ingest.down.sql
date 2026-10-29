-- migrations/000022_watchlist_feed_ingest.down.sql
DROP INDEX IF EXISTS uq_aml_watchlist_source_ref;
ALTER TABLE aml_watchlist DROP COLUMN IF EXISTS external_ref;
