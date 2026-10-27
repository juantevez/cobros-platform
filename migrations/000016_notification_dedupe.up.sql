-- migrations/000016_notification_dedupe.up.sql
-- Idempotencia de consumo (bug 3): clave natural UNIQUE para deduplicar
-- reentregas del mismo evento NATS. Sin esta constraint, un redelivery
-- duplicaba el email Y la fila en notifications.

ALTER TABLE notifications ADD COLUMN nats_msg_id TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN notifications.nats_msg_id IS
    'ID del mensaje NATS (Nats-Msg-Id = EventID del evento de dominio). '
    'Clave de dedupe junto con (tenant_id, event_type). Vacío = notificación '
    'no originada por un evento consumido (p. ej. envío manual futuro).';

-- Clave natural de idempotencia: una sola fila por (tenant, evento, mensaje).
-- Las filas sin msg id (vacío) quedan excluidas del índice parcial.
CREATE UNIQUE INDEX uq_notifications_tenant_event_msg
    ON notifications (tenant_id, event_type, nats_msg_id)
    WHERE nats_msg_id <> '';

-- Preferencias (bug 9): normalizar filas legacy escritas con sufijo de versión
-- ("payment.captured.v1" → "payment.captured") para que la clave guardada sea
-- exactamente la que muestran GET y busca el consumer. Si colisionan varias
-- versiones bajo la misma clave normalizada, se conserva la más reciente.
UPDATE notification_preferences
SET event_type = replace(event_type, '.v1', '')
WHERE event_type LIKE '%.v1'
  AND NOT EXISTS (
      SELECT 1 FROM notification_preferences np2
      WHERE np2.tenant_id = notification_preferences.tenant_id
        AND np2.channel   = notification_preferences.channel
        AND np2.event_type = replace(notification_preferences.event_type, '.v1', '')
        AND np2.updated_at > notification_preferences.updated_at
  );

DELETE FROM notification_preferences
WHERE event_type LIKE '%.v1';
