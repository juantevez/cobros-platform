-- migrations/000038_ledger_posting_position.up.sql
-- Orden de las líneas dentro de un asiento. Los postings se cargaban con
-- ORDER BY id (UUID aleatorio) y el asiento no conservaba el orden en que se
-- registró. Los asientos existentes toman ese mismo orden (no hay otro).

ALTER TABLE postings ADD COLUMN position SMALLINT;

UPDATE postings p
SET position = o.position
FROM (
    SELECT id, (row_number() OVER (PARTITION BY entry_id ORDER BY id) - 1)::smallint AS position
    FROM postings
) o
WHERE p.id = o.id;

ALTER TABLE postings ALTER COLUMN position SET NOT NULL;

-- Reemplaza al índice por entry_id: también sirve para cargar los postings.
DROP INDEX idx_postings_entry;
CREATE UNIQUE INDEX uq_postings_entry_position ON postings (entry_id, position);
