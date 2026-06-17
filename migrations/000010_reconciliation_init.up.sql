-- migrations/000010_reconciliation_init.up.sql
-- Contexto de Reconciliación.

CREATE TABLE reconciliation_runs (
    id                UUID        PRIMARY KEY,
    tenant_id         UUID,                    -- NULL para reconciliación global
    type              TEXT        NOT NULL
                                  CHECK (type IN ('payment','internal_ledger')),
    status            TEXT        NOT NULL
                                  CHECK (status IN ('pending','running','completed','failed')),
    period_from       TIMESTAMPTZ NOT NULL,
    period_to         TIMESTAMPTZ NOT NULL,
    total_records     INT         NOT NULL DEFAULT 0,
    matched_count     INT         NOT NULL DEFAULT 0,
    discrepancy_count INT         NOT NULL DEFAULT 0,
    error_msg         TEXT,
    created_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ,
    CHECK (period_from < period_to)
);

CREATE INDEX idx_recon_runs_tenant ON reconciliation_runs (tenant_id, created_at DESC)
    WHERE tenant_id IS NOT NULL;

CREATE INDEX idx_recon_runs_created ON reconciliation_runs (created_at DESC);

CREATE TABLE reconciliation_discrepancies (
    id             UUID        PRIMARY KEY,
    run_id         UUID        NOT NULL REFERENCES reconciliation_runs(id),
    tenant_id      UUID,
    type           TEXT        NOT NULL
                               CHECK (type IN (
                                   'missing_in_psp','missing_in_system',
                                   'amount_mismatch','status_mismatch',
                                   'ledger_imbalance'
                               )),
    record_id      TEXT        NOT NULL,   -- psp_reference, payment_id, etc.
    system_value   TEXT,                   -- valor en el sistema
    external_value TEXT,                   -- valor en el informe externo
    status         TEXT        NOT NULL DEFAULT 'open'
                               CHECK (status IN ('open','resolved','ignored')),
    notes          TEXT,
    resolved_by    TEXT,
    resolved_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_recon_disc_run    ON reconciliation_discrepancies (run_id, status);
CREATE INDEX idx_recon_disc_tenant ON reconciliation_discrepancies (tenant_id, created_at DESC)
    WHERE tenant_id IS NOT NULL;

COMMENT ON TABLE reconciliation_runs IS
    'Ejecuciones del proceso de reconciliación. Una por período y tipo.';
COMMENT ON TABLE reconciliation_discrepancies IS
    'Inconsistencias encontradas en cada run. Investigar y resolver manualmente.';
COMMENT ON COLUMN reconciliation_discrepancies.system_value IS
    'Valor registrado en el sistema (JSON o string descriptivo).';
COMMENT ON COLUMN reconciliation_discrepancies.external_value IS
    'Valor reportado por el PSP o banco (CSV o API).';
