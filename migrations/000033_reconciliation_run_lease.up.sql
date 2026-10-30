-- migrations/000033_reconciliation_run_lease.up.sql
-- Runs trabados en running: si el proceso muere entre el claim y el cierre,
-- el run quedaba running para siempre.
--   - started_at: cuándo se tomó el run. Pasado un umbral (RunStaleAfter en
--     el dominio) se considera abandonado y se puede volver a tomar.
--   - version: optimistic locking. Cada UPDATE exige la versión leída y la
--     incrementa, así dos procesos que retoman el mismo run abandonado no
--     pueden ganar ambos.

ALTER TABLE reconciliation_runs
    ADD COLUMN started_at TIMESTAMPTZ,
    ADD COLUMN version    BIGINT NOT NULL DEFAULT 0;
