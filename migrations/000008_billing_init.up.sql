-- migrations/000008_billing_init.up.sql
-- Contexto de Billing & Fees: planes de tarifas y asignaciones por tenant.

-- ── Catálogo de planes ────────────────────────────────────────────────────────
-- Sin RLS: los planes son globales de la plataforma.

CREATE TABLE billing_plans (
    id               UUID        PRIMARY KEY,
    name             TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    base_rate_bps    BIGINT      NOT NULL DEFAULT 0
                                 CHECK (base_rate_bps BETWEEN 0 AND 10000),
    base_fixed_amount BIGINT     NOT NULL DEFAULT 0
                                 CHECK (base_fixed_amount >= 0),
    monthly_fee      BIGINT      NOT NULL DEFAULT 0
                                 CHECK (monthly_fee >= 0),
    currency         CHAR(3)     NOT NULL,
    active           BOOLEAN     NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL
);

-- Overrides por método de pago dentro de un plan.
-- Ej: plan "Pro" cobra 2% para wallets y 2.5% para tarjetas.
CREATE TABLE billing_plan_method_rates (
    plan_id      UUID    NOT NULL REFERENCES billing_plans(id) ON DELETE CASCADE,
    method       TEXT    NOT NULL CHECK (method IN ('card','wallet','transfer','qr')),
    rate_bps     BIGINT  NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
    fixed_amount BIGINT  NOT NULL DEFAULT 0 CHECK (fixed_amount >= 0),
    PRIMARY KEY (plan_id, method)
);

-- ── Asignaciones por tenant ───────────────────────────────────────────────────
-- Con RLS: cada tenant solo ve sus propias asignaciones.

CREATE TABLE billing_tenant_plans (
    id                  UUID        PRIMARY KEY,
    tenant_id           UUID        NOT NULL,
    plan_id             UUID        NOT NULL REFERENCES billing_plans(id),
    -- Snapshot del nombre al momento de asignar (para reportes históricos).
    plan_name           TEXT        NOT NULL,
    -- Overrides negociados para este tenant (NULL = usar los del plan base).
    custom_rate_bps     BIGINT      CHECK (custom_rate_bps BETWEEN 0 AND 10000),
    custom_fixed_amount BIGINT      CHECK (custom_fixed_amount >= 0),
    active              BOOLEAN     NOT NULL DEFAULT true,
    valid_from          TIMESTAMPTZ NOT NULL,
    valid_until         TIMESTAMPTZ, -- NULL = vigente indefinidamente
    created_at          TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_billing_tenant_plans_active
    ON billing_tenant_plans (tenant_id)
    WHERE active = true;

CREATE INDEX idx_billing_tenant_plans_tenant
    ON billing_tenant_plans (tenant_id, valid_from DESC);

ALTER TABLE billing_tenant_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY billing_tenant_plans_isolation ON billing_tenant_plans
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE billing_plans IS
    'Catálogo de planes de tarifas. Sin RLS (planes globales de la plataforma).';
COMMENT ON TABLE billing_plan_method_rates IS
    'Overrides de tarifa por método de pago dentro de un plan.';
COMMENT ON TABLE billing_tenant_plans IS
    'Asignación de un plan a un tenant. Solo uno activo por tenant a la vez.';
COMMENT ON COLUMN billing_tenant_plans.custom_rate_bps IS
    'Override de tasa en bps negociado con este tenant. NULL = usar la del plan.';
COMMENT ON COLUMN billing_tenant_plans.plan_name IS
    'Snapshot del nombre del plan al momento de la asignación.';
