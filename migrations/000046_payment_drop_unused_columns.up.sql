-- migrations/000046_payment_drop_unused_columns.up.sql
-- Columnas que nunca se escribieron: authorized_at (no hay flujo de dos pasos,
-- autorizar y después capturar) y checkout_id (no existe el BC Checkout).
-- Si llegan esas funcionalidades, se agregan con su semántica.
ALTER TABLE payments
    DROP COLUMN IF EXISTS authorized_at,
    DROP COLUMN IF EXISTS checkout_id;
