-- migrations/000003_ledger_init.up.sql
-- Libro Mayor de doble partida (Ledger).
--
-- Principios:
--   - Append-only: sin UPDATE/DELETE en journal_entries ni postings.
--   - Doble partida: validada en dominio, reforzada por trigger.
--   - Idempotencia: UNIQUE(tenant_id, idempotency_key) en journal_entries.
--   - Saldos: actualizados transaccionalmente en account_balances.
--   - RLS: habilitado en todas las tablas con tenant_id.

-- ── Cuentas contables ─────────────────────────────────────────────────────────

CREATE TABLE ledger_accounts (
    id          UUID        PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    type        TEXT        NOT NULL
                            CHECK (type IN (
                                'merchant_balance', 'platform_fees',
                                'reserve', 'in_transit', 'dispute_hold'
                            )),
    currency    CHAR(3)     NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_ledger_accounts_tenant_type_currency
    ON ledger_accounts (tenant_id, type, currency);

ALTER TABLE ledger_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY ledger_accounts_tenant_isolation ON ledger_accounts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- ── Saldos (proyección actualizada transaccionalmente) ────────────────────────

CREATE TABLE account_balances (
    account_id  UUID        PRIMARY KEY REFERENCES ledger_accounts(id),
    balance     BIGINT      NOT NULL DEFAULT 0,  -- en centavos; puede ser negativo
    updated_at  TIMESTAMPTZ NOT NULL
);

-- ── Asientos (Journal Entries) ────────────────────────────────────────────────

CREATE TABLE journal_entries (
    id              UUID        PRIMARY KEY,
    tenant_id       UUID        NOT NULL,
    idempotency_key TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    metadata        JSONB       NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    -- Idempotencia: un solo asiento por clave dentro del tenant.
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_journal_entries_tenant_created
    ON journal_entries (tenant_id, created_at DESC);

ALTER TABLE journal_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY journal_entries_tenant_isolation ON journal_entries
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- ── Líneas de asiento (Postings) ──────────────────────────────────────────────

CREATE TABLE postings (
    id          UUID        PRIMARY KEY,
    entry_id    UUID        NOT NULL REFERENCES journal_entries(id),
    tenant_id   UUID        NOT NULL,  -- desnormalizado para RLS
    account_id  UUID        NOT NULL REFERENCES ledger_accounts(id),
    direction   TEXT        NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount      BIGINT      NOT NULL CHECK (amount > 0),
    currency    CHAR(3)     NOT NULL
);

CREATE INDEX idx_postings_entry ON postings (entry_id);

ALTER TABLE postings ENABLE ROW LEVEL SECURITY;
CREATE POLICY postings_tenant_isolation ON postings
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

-- ── Trigger: verificar doble partida en la base ───────────────────────────────
-- Segunda línea de defensa tras la validación en el dominio Go.
-- Se dispara al insertar todos los postings de un entry.

CREATE OR REPLACE FUNCTION check_entry_balance()
RETURNS TRIGGER AS $$
DECLARE
    v_debits  BIGINT;
    v_credits BIGINT;
BEGIN
    SELECT
        COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0),
        COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0)
    INTO v_debits, v_credits
    FROM postings
    WHERE entry_id = NEW.entry_id;

    -- Solo validamos cuando hay al menos 2 postings (entry completo).
    IF (SELECT COUNT(*) FROM postings WHERE entry_id = NEW.entry_id) >= 2
        AND v_debits != v_credits THEN
        RAISE EXCEPTION 'journal entry % is not balanced: debits=% credits=%',
            NEW.entry_id, v_debits, v_credits;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_check_entry_balance
    AFTER INSERT ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_entry_balance();

COMMENT ON TABLE journal_entries IS 'Asientos contables de doble partida. Append-only.';
COMMENT ON TABLE postings        IS 'Líneas de cada asiento. Append-only.';
COMMENT ON TABLE account_balances IS 'Saldos actualizados transaccionalmente. Proyección de postings.';
