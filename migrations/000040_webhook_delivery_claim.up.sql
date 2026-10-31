-- migrations/000040_webhook_delivery_claim.up.sql
-- Reclamo de una delivery antes del POST al comercio.
--
-- El RetryPoller y el reintento manual (y dos réplicas del worker) podían
-- despachar la misma delivery en paralelo: doble POST, choque en
-- UNIQUE (delivery_id, attempt_num) y last-write-wins que volvía un delivered
-- a failed. Antes de despachar, el proceso fija claimed_until con un UPDATE
-- condicional; solo uno gana. Si el proceso muere a mitad del envío, el
-- reclamo vence y el poller la retoma.
ALTER TABLE webhook_deliveries ADD COLUMN claimed_until TIMESTAMPTZ;

COMMENT ON COLUMN webhook_deliveries.claimed_until IS
    'Hasta cuándo un proceso tiene reclamada la delivery para despacharla. NULL = libre.';
