-- migrations/000012_dispute_init.up.sql
-- Contexto de Disputes & Chargebacks.

CREATE TABLE disputes (
    id            UUID        PRIMARY KEY,
    tenant_id     UUID        NOT NULL,
    payment_id    TEXT        NOT NULL,
    psp_reference TEXT,
    amount        BIGINT      NOT NULL CHECK (amount > 0),
    currency      CHAR(3)     NOT NULL,
    reason        TEXT        NOT NULL
                              CHECK (reason IN (
                                  'fraudulent','product_not_received',
                                  'product_unacceptable','duplicate',
                                  'credit_not_processed','general'
                              )),
    status        TEXT        NOT NULL
                              CHECK (status IN (
                                  'open','under_review','won',
                                  'lost','accepted','expired'
                              )),
    response_note TEXT,
    resolved_note TEXT,
    deadline      TIMESTAMPTZ NOT NULL,
    opened_at     TIMESTAMPTZ NOT NULL,
    responded_at  TIMESTAMPTZ,
    resolved_at   TIMESTAMPTZ,
    -- Un pago solo puede tener una disputa activa.
    UNIQUE (payment_id)
);

CREATE INDEX idx_disputes_tenant_status
    ON disputes (tenant_id, status, opened_at DESC);

-- Índice para el ExpiryPoller: disputes abiertas con deadline pasado.
CREATE INDEX idx_disputes_overdue
    ON disputes (deadline)
    WHERE status = 'open';

ALTER TABLE disputes ENABLE ROW LEVEL SECURITY;
CREATE POLICY disputes_tenant_isolation ON disputes
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- Evidencia presentada por el comercio para contestar la disputa.
CREATE TABLE dispute_evidence (
    id            UUID        PRIMARY KEY,
    dispute_id    UUID        NOT NULL REFERENCES disputes(id),
    evidence_type TEXT        NOT NULL,  -- receipt | tracking | communication | other
    reference     TEXT        NOT NULL,  -- URL o ID externo del documento
    description   TEXT,
    submitted_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_dispute_evidence_dispute ON dispute_evidence (dispute_id);

COMMENT ON TABLE disputes IS
    'Disputas y chargebacks notificados por el banco. UNIQUE(payment_id).';
COMMENT ON TABLE dispute_evidence IS
    'Documentos enviados al banco para contestar la disputa.';
COMMENT ON COLUMN disputes.deadline IS
    'Fecha límite para que el comercio presente evidencia. Pasado este plazo → expired.';
