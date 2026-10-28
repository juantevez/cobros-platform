-- deploy/seed/compliance_watchlist.sql
-- Watchlist de EJEMPLO para desarrollo local (nombres ficticios). No correr en
-- producción: allí la watchlist se carga desde las listas oficiales
-- (OFAC/EU/UN) por un proceso aparte o vía POST /api/v1/compliance/watchlist.
-- Idempotente: reactiva/inserta solo si no hay una entrada vigente igual.
-- normalized_name debe respetar domain.NormalizeName.

INSERT INTO aml_watchlist (id, full_name, normalized_name, list_type, country, source, added_at)
SELECT gen_random_uuid(), v.full_name, v.normalized_name, v.list_type, v.country, v.source, now()
FROM (VALUES
    ('Vladimir Petrov',       'vladimir petrov',       'sanctions', 'RU', 'OFAC'),
    ('Ivan Sokolov',          'ivan sokolov',          'sanctions', 'RU', 'EU'),
    ('Kim Jong Trading Co',   'kim jong trading co',   'sanctions', 'KP', 'UN'),
    ('Global Shell Holdings', 'global shell holdings', 'pep',       'PA', 'EU')
) AS v(full_name, normalized_name, list_type, country, source)
WHERE NOT EXISTS (
    SELECT 1 FROM aml_watchlist w
    WHERE w.normalized_name = v.normalized_name
      AND w.list_type = v.list_type
      AND w.removed_at IS NULL
);
