-- migrations/000001_init_outbox.up.sql
-- Tabla del Transactional Outbox.
-- Compartida por todos los contextos que publican eventos de dominio.

CREATE TABLE outbox_messages (
    id           TEXT        PRIMARY KEY,          -- UUID del evento (Nats-Msg-Id)
    tenant_id    UUID,                             -- null = evento de plataforma
    subject      TEXT        NOT NULL,             -- ej: "auth.tenant.created.v1"
    payload      JSONB       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ                       -- null = pendiente
);

-- Índice parcial: solo los pendientes importan al relay.
CREATE INDEX idx_outbox_pending
    ON outbox_messages (created_at)
    WHERE published_at IS NULL;

COMMENT ON TABLE outbox_messages IS
    'Transactional Outbox: eventos de dominio pendientes de publicación en NATS JetStream.';
