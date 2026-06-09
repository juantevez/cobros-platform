-- migrations/000006_payment_init.up.sql
-- Contexto de Payment Processing.

CREATE TABLE payments (
    id              UUID        PRIMARY KEY,
    tenant_id       UUID        NOT NULL,
    idempotency_key TEXT        NOT NULL,
    checkout_id     TEXT,

    -- Montos (en centavos)
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    currency        CHAR(3)     NOT NULL,
    platform_fee    BIGINT      NOT NULL DEFAULT 0,
    psp_fee         BIGINT      NOT NULL DEFAULT 0,

    -- Datos del pagador (nunca datos de tarjeta en crudo)
    payer_name      TEXT,
    payer_email     TEXT,
    payer_doc_type  TEXT,
    payer_doc_num   TEXT,

    -- PSP
    payment_method  TEXT        NOT NULL,
    psp_name        TEXT,
    psp_reference   TEXT,

    -- Estado
    status          TEXT        NOT NULL
                                CHECK (status IN (
                                    'initiated', 'processing', 'captured',
                                    'risk_rejected', 'failed', 'refunded'
                                )),
    failure_reason  TEXT,
    metadata        JSONB       NOT NULL DEFAULT '{}',

    -- Timestamps
    authorized_at   TIMESTAMPTZ,
    captured_at     TIMESTAMPTZ,
    failed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,

    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_payments_tenant_status
    ON payments (tenant_id, status, created_at DESC);

CREATE INDEX idx_payments_psp_ref
    ON payments (psp_reference)
    WHERE psp_reference IS NOT NULL;

ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY payments_tenant_isolation ON payments
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE payments IS
    'Pagos procesados por la plataforma. Append-friendly: solo UPDATE de status.';
COMMENT ON COLUMN payments.payer_name IS
    'Datos del pagador. NUNCA almacenar datos de tarjeta en crudo aquí.';
COMMENT ON COLUMN payments.idempotency_key IS
    'Garantiza que el mismo cobro no se procesa dos veces.';
