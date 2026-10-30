-- migrations/000033_reconciliation_run_lease.down.sql
ALTER TABLE reconciliation_runs
    DROP COLUMN version,
    DROP COLUMN started_at;
