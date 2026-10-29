-- migrations/000023_watchlist_fuzzy.down.sql
-- La extensión no se elimina: puede estar en uso por otros objetos.
DROP INDEX IF EXISTS idx_aml_watchlist_trgm;
