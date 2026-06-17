-- migrations/000011_notification_init.up.sql
-- Contexto de Notifications: log de envíos y preferencias por tenant.

CREATE TABLE notifications (
    id              UUID        PRIMARY KEY,
    tenant_id       UUID        NOT NULL,
    event_type      TEXT        NOT NULL,
    channel         TEXT        NOT NULL DEFAULT 'email',
    recipient_email TEXT        NOT NULL,
    subject         TEXT        NOT NULL,
    status          TEXT        NOT NULL
                                CHECK (status IN ('pending','sent','failed')),
    error_msg       TEXT,
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_notifications_tenant ON notifications (tenant_id, created_at DESC);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY notifications_isolation ON notifications
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- Preferencias: qué notificaciones quiere recibir cada tenant y a qué email.
-- Una fila por (tenant, event_type, channel). Sin fila = default habilitado.
CREATE TABLE notification_preferences (
    tenant_id       UUID    NOT NULL,
    event_type      TEXT    NOT NULL,
    channel         TEXT    NOT NULL DEFAULT 'email',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    recipient_email TEXT,        -- NULL = usar email del admin del tenant
    updated_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, event_type, channel)
);

ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY;
CREATE POLICY notification_prefs_isolation ON notification_preferences
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE notifications IS
    'Log de notificaciones enviadas. Una fila por intento de envío.';
COMMENT ON TABLE notification_preferences IS
    'Preferencias por tenant. Sin fila = habilitado por defecto para ese evento.';
COMMENT ON COLUMN notification_preferences.recipient_email IS
    'Email destino override. NULL = usar email del admin del tenant.';
