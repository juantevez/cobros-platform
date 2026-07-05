-- migrations/000013_reporting_init.up.sql
-- Contexto de Reporting (read-model CQRS): proyecciones para Dashboard & Reporting.
--
-- Diseño: en vez de mantener contadores incrementales (que se duplicarían ante
-- la re-entrega de eventos de JetStream), guardamos HECHOS INMUTABLES —una fila
-- por payment y una por posting— con inserción idempotente (ON CONFLICT DO NOTHING)
-- y agregamos en tiempo de consulta. Esto respeta el espíritu append-only del
-- ledger y hace la proyección naturalmente idempotente.

-- report_payment_fact: un hecho por pago capturado.
-- Fuente: payment.captured.v1. Cubre volumen transaccional y fees/revenue.
CREATE TABLE report_payment_fact (
    payment_id     UUID        PRIMARY KEY,      -- idempotencia por pago
    tenant_id      UUID        NOT NULL,
    currency       TEXT        NOT NULL,
    amount         BIGINT      NOT NULL,         -- monto bruto en centavos
    platform_fee   BIGINT      NOT NULL DEFAULT 0,
    psp_fee        BIGINT      NOT NULL DEFAULT 0,
    payment_method TEXT        NOT NULL DEFAULT '',
    captured_at    TIMESTAMPTZ NOT NULL          -- momento de proyección
);

CREATE INDEX idx_report_payment_fact_tenant
    ON report_payment_fact (tenant_id, captured_at);

ALTER TABLE report_payment_fact ENABLE ROW LEVEL SECURITY;
CREATE POLICY report_payment_fact_isolation ON report_payment_fact
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- report_ledger_movement: un hecho por posting de un asiento confirmado.
-- Fuente: ledger.entry.posted.v1. Cubre balances por comercio.
-- La PK compuesta (entry_id, account_id, direction) hace idempotente la
-- proyección: un asiento re-entregado no duplica movimientos.
CREATE TABLE report_ledger_movement (
    entry_id     UUID        NOT NULL,
    account_id   UUID        NOT NULL,
    direction    TEXT        NOT NULL CHECK (direction IN ('debit','credit')),
    tenant_id    UUID        NOT NULL,
    account_type TEXT        NOT NULL DEFAULT '',
    currency     TEXT        NOT NULL,
    amount       BIGINT      NOT NULL,           -- monto en centavos (siempre > 0)
    posted_at    TIMESTAMPTZ NOT NULL,           -- momento de proyección
    PRIMARY KEY (entry_id, account_id, direction)
);

CREATE INDEX idx_report_ledger_movement_tenant
    ON report_ledger_movement (tenant_id, account_type, currency);

ALTER TABLE report_ledger_movement ENABLE ROW LEVEL SECURITY;
CREATE POLICY report_ledger_movement_isolation ON report_ledger_movement
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE report_payment_fact IS
    'Read-model: un hecho inmutable por pago capturado. Agregado en query para volumen y revenue.';
COMMENT ON TABLE report_ledger_movement IS
    'Read-model: un hecho inmutable por posting. Agregado en query para balances por comercio.';
