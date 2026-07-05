-- migrations/000014_compliance_init.up.sql
-- Contexto de Compliance & AML: screening de listas + monitoreo transaccional.

-- aml_watchlist: lista de vigilancia GLOBAL de la plataforma (sanciones/PEP).
-- No es multi-tenant: es una lista única contra la que se hace screening de
-- todos los comercios. Sin RLS. Se siembra con datos de ejemplo y puede
-- gestionarse por el endpoint admin.
CREATE TABLE aml_watchlist (
    id              UUID        PRIMARY KEY,
    full_name       TEXT        NOT NULL,
    normalized_name TEXT        NOT NULL,   -- lowercase + espacios colapsados
    list_type       TEXT        NOT NULL
                                CHECK (list_type IN ('sanctions','pep')),
    country         TEXT        NOT NULL DEFAULT '',
    source          TEXT        NOT NULL DEFAULT '',  -- OFAC, EU, UN, etc.
    added_at        TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_aml_watchlist_normalized ON aml_watchlist (normalized_name);

-- aml_alerts: alertas de compliance por comercio (tenant-scoped, con RLS).
-- Estado: open → cleared (falso positivo) | confirmed (verdadero positivo).
-- La unicidad (tenant_id, alert_type, subject) hace idempotente la generación
-- de alertas ante re-entregas de eventos de JetStream.
CREATE TABLE aml_alerts (
    id          UUID        PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    alert_type  TEXT        NOT NULL
                            CHECK (alert_type IN (
                                'sanctions_match',
                                'transaction_threshold',
                                'transaction_velocity'
                            )),
    risk_level  TEXT        NOT NULL CHECK (risk_level IN ('low','medium','high')),
    status      TEXT        NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','cleared','confirmed')),
    subject     TEXT        NOT NULL,   -- legal_name o payment_id que disparó la alerta
    score       INT         NOT NULL DEFAULT 0,
    details     JSONB       NOT NULL DEFAULT '{}',
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    UNIQUE (tenant_id, alert_type, subject)
);

CREATE INDEX idx_aml_alerts_tenant  ON aml_alerts (tenant_id, created_at DESC);
CREATE INDEX idx_aml_alerts_status  ON aml_alerts (tenant_id, status);

ALTER TABLE aml_alerts ENABLE ROW LEVEL SECURITY;
CREATE POLICY aml_alerts_isolation ON aml_alerts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- Semilla de la watchlist (nombres de ejemplo, no reales).
INSERT INTO aml_watchlist (id, full_name, normalized_name, list_type, country, source, added_at) VALUES
    (gen_random_uuid(), 'Vladimir Petrov',      'vladimir petrov',      'sanctions', 'RU', 'OFAC', now()),
    (gen_random_uuid(), 'Ivan Sokolov',         'ivan sokolov',         'sanctions', 'RU', 'EU',   now()),
    (gen_random_uuid(), 'Kim Jong Trading Co',  'kim jong trading co',  'sanctions', 'KP', 'UN',   now()),
    (gen_random_uuid(), 'Global Shell Holdings','global shell holdings','pep',       'PA', 'EU',   now());

COMMENT ON TABLE aml_watchlist IS
    'Lista de vigilancia global (sanciones/PEP). No multi-tenant; screening de todos los comercios.';
COMMENT ON TABLE aml_alerts IS
    'Alertas de compliance por comercio. Unicidad (tenant, tipo, subject) = idempotencia.';
