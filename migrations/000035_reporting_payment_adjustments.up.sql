-- migrations/000035_reporting_payment_adjustments.up.sql
-- Ajustes que descuentan del bruto capturado en el revenue: reembolsos
-- (payment.refunded.v1) y contracargos (dispute.resolved.v1 perdida, aceptada
-- o vencida). Hechos inmutables con inserción idempotente, igual que el resto
-- del read-model.
--   - refund:     source_id = payment_id (un reembolso, total, por pago)
--   - chargeback: source_id = dispute_id

CREATE TABLE report_payment_adjustment (
    kind        TEXT        NOT NULL CHECK (kind IN ('refund','chargeback')),
    source_id   UUID        NOT NULL,
    payment_id  UUID        NOT NULL,
    tenant_id   UUID        NOT NULL,
    currency    TEXT        NOT NULL,
    amount      BIGINT      NOT NULL,          -- centavos, siempre > 0
    occurred_at TIMESTAMPTZ NOT NULL,          -- hora del evento de origen
    PRIMARY KEY (kind, source_id)
);

CREATE INDEX idx_report_payment_adjustment_tenant
    ON report_payment_adjustment (tenant_id, occurred_at);

ALTER TABLE report_payment_adjustment ENABLE ROW LEVEL SECURITY;
CREATE POLICY report_payment_adjustment_isolation ON report_payment_adjustment
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

COMMENT ON TABLE report_payment_adjustment IS
    'Read-model: reembolsos y contracargos. Se restan del bruto en el revenue.';
