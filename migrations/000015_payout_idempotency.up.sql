-- migrations/000015_payout_idempotency.up.sql
-- Correcciones de limitaciones del BC Payout (ver docs/technical/PAYOUT.MD §10):
--   1. Idempotencia de negocio: evita desembolsos duplicados por retries.
--   2. Nuevo estado 'unknown': timeout/error de red del banco ya NO se trata
--      como fallo definitivo; requiere resolución por reconciliación.

ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_status_check;
ALTER TABLE payouts
    ADD CONSTRAINT payouts_status_check
    CHECK (status IN ('initiated','processing','confirmed','failed','unknown'));

ALTER TABLE payouts
    ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';

-- Clave obligatoria y única por tenant: dos requests concurrentes con la
-- misma key colisionan en el UNIQUE y el segundo recibe ErrPayoutAlreadyExists.
CREATE UNIQUE INDEX uq_payouts_tenant_idempotency
    ON payouts (tenant_id, idempotency_key)
    WHERE idempotency_key <> '';

COMMENT ON COLUMN payouts.idempotency_key IS
    'Clave de idempotencia de negocio (header Idempotency-Key). Un retry con la misma clave retorna el payout existente en lugar de desembolsar dos veces.';
