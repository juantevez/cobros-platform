-- migrations/000020_compliance_holds.up.sql
-- Compliance & AML — enforcement: hold preventivo y reversible por
-- (tenant, alerta), en el contexto Auth. Lo pone/levanta el consumer de
-- compliance.sanction.* y lo leen los gates de Payment y Payout.
-- Ver docs/technical/COMPLIANCE.md §7.3.

CREATE TABLE tenant_compliance_holds (
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    alert_id    UUID        NOT NULL,   -- aml_alerts.id que originó el hold
    placed_at   TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,            -- NULL = hold vigente
    PRIMARY KEY (tenant_id, alert_id)
);

CREATE INDEX idx_tenant_compliance_holds_active
    ON tenant_compliance_holds (tenant_id) WHERE released_at IS NULL;

COMMENT ON TABLE tenant_compliance_holds IS
    'Holds preventivos de cumplimiento (match exacto en sanciones). Vigente = released_at NULL. Bloquea cobros y payouts; no cambia tenants.status.';
