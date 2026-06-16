-- migrations/000009_webhook_init.up.sql
-- Contexto de Webhooks: entrega de eventos al comercio via HTTP.

-- ── Endpoints ─────────────────────────────────────────────────────────────────
-- URL del comercio donde la plataforma envía notificaciones.
-- El secret se usa para calcular la firma HMAC-SHA256 de cada entrega.

CREATE TABLE webhook_endpoints (
    id          UUID        PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    url         TEXT        NOT NULL,
    -- Secret HMAC en texto claro. En producción usar AES-256-GCM.
    secret      TEXT        NOT NULL,
    -- Hint: últimos 4 chars del secret, para identificación sin exponer el valor.
    secret_hint TEXT        NOT NULL,
    -- Array de event types suscritos, ej: {"payment.captured","payout.confirmed"}
    events      TEXT[]      NOT NULL,
    active      BOOLEAN     NOT NULL DEFAULT true,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_webhook_endpoints_tenant
    ON webhook_endpoints (tenant_id, active);

-- Índice GIN para búsqueda eficiente por contenido del array events.
CREATE INDEX idx_webhook_endpoints_events
    ON webhook_endpoints USING GIN (events);

ALTER TABLE webhook_endpoints ENABLE ROW LEVEL SECURITY;
CREATE POLICY webhook_endpoints_isolation ON webhook_endpoints
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- ── Deliveries ────────────────────────────────────────────────────────────────
-- Una delivery representa el intento de entrega de un evento a un endpoint.
-- Idempotencia: UNIQUE(endpoint_id, event_id) garantiza que el mismo evento
-- no genera dos deliveries para el mismo endpoint.

CREATE TABLE webhook_deliveries (
    id            UUID        PRIMARY KEY,
    endpoint_id   UUID        NOT NULL REFERENCES webhook_endpoints(id),
    tenant_id     UUID        NOT NULL,
    event_type    TEXT        NOT NULL,  -- normalizado sin versión, ej: "payment.captured"
    event_id      TEXT        NOT NULL,  -- ID del evento de dominio original
    payload       JSONB       NOT NULL,  -- envelope completo que se envía al comercio
    status        TEXT        NOT NULL
                              CHECK (status IN ('pending','delivered','failed','exhausted')),
    attempt_count INT         NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,           -- NULL cuando está delivered o exhausted
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    -- Un evento solo genera una delivery por endpoint (idempotencia).
    UNIQUE (endpoint_id, event_id)
);

-- Índice para el RetryPoller: busca deliveries listas para reintento.
CREATE INDEX idx_webhook_deliveries_retry
    ON webhook_deliveries (next_retry_at)
    WHERE status IN ('pending', 'failed') AND next_retry_at IS NOT NULL;

CREATE INDEX idx_webhook_deliveries_tenant_created
    ON webhook_deliveries (tenant_id, created_at DESC);

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY webhook_deliveries_isolation ON webhook_deliveries
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- ── Delivery Attempts ─────────────────────────────────────────────────────────
-- Historial de cada intento HTTP individual.

CREATE TABLE webhook_delivery_attempts (
    id            UUID        PRIMARY KEY,
    delivery_id   UUID        NOT NULL REFERENCES webhook_deliveries(id),
    attempt_num   INT         NOT NULL,
    http_status   INT,                  -- NULL si error de red o timeout
    response_body TEXT,                 -- primeros 500 chars de la respuesta
    error_msg     TEXT,                 -- mensaje si no hubo respuesta HTTP
    duration_ms   BIGINT      NOT NULL DEFAULT 0,
    attempted_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (delivery_id, attempt_num)
);

CREATE INDEX idx_webhook_attempts_delivery
    ON webhook_delivery_attempts (delivery_id, attempt_num);

COMMENT ON TABLE webhook_endpoints IS
    'URLs de los comercios para recibir notificaciones de eventos via HTTP.';
COMMENT ON TABLE webhook_deliveries IS
    'Intentos de entrega de eventos. UNIQUE(endpoint_id, event_id) garantiza idempotencia.';
COMMENT ON TABLE webhook_delivery_attempts IS
    'Historial de cada intento HTTP. Útil para debugging de integraciones.';
COMMENT ON COLUMN webhook_endpoints.secret IS
    'Secret HMAC. El comercio lo usa para verificar X-Cobros-Signature.';
COMMENT ON COLUMN webhook_deliveries.next_retry_at IS
    'Próximo reintento programado. El RetryPoller busca WHERE next_retry_at <= now.';
