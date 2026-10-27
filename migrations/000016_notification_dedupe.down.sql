-- migrations/000016_notification_dedupe.down.sql
DROP INDEX IF EXISTS uq_notifications_tenant_event_msg;
ALTER TABLE notifications DROP COLUMN IF EXISTS nats_msg_id;
