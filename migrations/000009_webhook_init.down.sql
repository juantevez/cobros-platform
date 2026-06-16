-- migrations/000009_webhook_init.down.sql
DROP TABLE IF EXISTS webhook_delivery_attempts;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
