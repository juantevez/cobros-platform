-- migrations/000007_payout_init.up.sql
-- Contexto de Payouts / Desembolsos.

CREATE TABLE payouts (
    id               UUID        PRIMARY KEY,
    tenant_id        UUID        NOT NULL,

    -- Monto desembolsado (en centavos)
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    currency         CHAR(3)     NOT NULL,

    -- Cuenta bancaria destino (snapshot al momento del payout)
    bank_acct_type   TEXT        NOT NULL,
    bank_acct_num    TEXT        NOT NULL,
    bank_name        TEXT,
    holder_name      TEXT        NOT NULL,

    -- Estado
    status           TEXT        NOT NULL
                                 CHECK (status IN ('initiated','processing','confirmed','failed')),
    bank_reference   TEXT,        -- referencia del banco al confirmar
    failure_reason   TEXT,
    ledger_entry_key TEXT        NOT NULL,  -- clave de idempotencia del asiento

    -- Timestamps
    initiated_at     TIMESTAMPTZ,
    confirmed_at     TIMESTAMPTZ,
    failed_at        TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_payouts_tenant_status
    ON payouts (tenant_id, status, created_at DESC);

ALTER TABLE payouts ENABLE ROW LEVEL SECURITY;
CREATE POLICY payouts_tenant_isolation ON payouts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- Actualizar el CHECK de ledger_accounts para incluir los nuevos tipos.
-- Los AccountType payout_transit y payout_sent se crean vía Onboarding consumer.
ALTER TABLE ledger_accounts
    DROP CONSTRAINT IF EXISTS ledger_accounts_type_check;

ALTER TABLE ledger_accounts
    ADD CONSTRAINT ledger_accounts_type_check
    CHECK (type IN (
        'merchant_balance', 'platform_fees',
        'reserve', 'in_transit', 'dispute_hold',
        'payout_transit', 'payout_sent'
    ));

COMMENT ON TABLE payouts IS
    'Desembolsos de fondos al comercio. Cada payout genera asientos en el Ledger.';
COMMENT ON COLUMN payouts.ledger_entry_key IS
    'Clave de idempotencia del asiento contable correspondiente en journal_entries.';
