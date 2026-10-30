-- migrations/000038_ledger_posting_position.down.sql
DROP INDEX uq_postings_entry_position;
CREATE INDEX idx_postings_entry ON postings (entry_id);
ALTER TABLE postings DROP COLUMN position;
