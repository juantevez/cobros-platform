-- migrations/000042_dispute_version.up.sql
-- Concurrencia optimista en disputes.
--
-- El dominio y el puerto ya describían un UPDATE ... WHERE version = <leída>,
-- pero la columna no existía y Update filtraba solo por id: dos transiciones
-- concurrentes (contestar y aceptar, resolver dos veces, aceptar justo cuando
-- el poller la vence) ganaban las dos y publicaban dos dispute.resolved.v1.
ALTER TABLE disputes ADD COLUMN version BIGINT NOT NULL DEFAULT 1;

COMMENT ON COLUMN disputes.version IS
    'Versión para concurrencia optimista: cada transición la incrementa y el UPDATE exige la leída.';
