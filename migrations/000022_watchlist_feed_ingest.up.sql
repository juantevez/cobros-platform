-- migrations/000022_watchlist_feed_ingest.up.sql
-- Ingesta de feeds de watchlist (POST /compliance/watchlist/import).
-- external_ref: id de la entrada en el feed de origen; NULL = carga manual.
-- Único por fuente entre las vigentes: base del delta (alta/cambio/baja).

ALTER TABLE aml_watchlist ADD COLUMN external_ref TEXT;

CREATE UNIQUE INDEX uq_aml_watchlist_source_ref
    ON aml_watchlist (source, external_ref)
    WHERE removed_at IS NULL AND external_ref IS NOT NULL;
