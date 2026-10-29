-- migrations/000023_watchlist_fuzzy.up.sql
-- Screening difuso (typos/transliteraciones). El repositorio preselecciona
-- candidatos buscando cada token del nombre evaluado dentro de las entradas
-- (normalized_name %> token, pg_trgm word_similarity con umbral local) y la
-- decisión final se toma en Go (domain/fuzzy.go).
--
-- Requiere poder ejecutar CREATE EXTENSION (en Postgres administrado, un rol
-- con ese privilegio). El índice GIN trigram cubre esa dirección de búsqueda:
-- con 50k entradas la preselección cuesta ~5-10 ms (sin índice, ~1 s).

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX idx_aml_watchlist_trgm
    ON aml_watchlist USING GIN (normalized_name gin_trgm_ops)
    WHERE removed_at IS NULL;
