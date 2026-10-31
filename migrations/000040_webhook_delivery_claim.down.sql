-- migrations/000040_webhook_delivery_claim.down.sql
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS claimed_until;
