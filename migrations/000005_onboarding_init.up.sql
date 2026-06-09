-- migrations/000005_onboarding_init.up.sql
-- Contexto de Onboarding (KYC/KYB).
-- RLS habilitado en todas las tablas del comercio.

CREATE TABLE onboarding_applications (
    id                UUID        PRIMARY KEY,
    tenant_id         UUID        NOT NULL,
    status            TEXT        NOT NULL
                                  CHECK (status IN ('pending','in_review','approved',
                                                    'rejected','requires_more_info')),
    -- Datos del negocio
    legal_name        TEXT        NOT NULL,
    tax_id            TEXT        NOT NULL,
    business_category TEXT        NOT NULL,
    -- Dirección
    addr_street       TEXT,
    addr_city         TEXT,
    addr_state        TEXT,
    addr_country      TEXT,
    addr_postal       TEXT,
    -- Contacto
    website           TEXT,
    phone_number      TEXT,
    -- Revisión
    review_notes      TEXT,
    rejection_reason  TEXT,
    -- Timestamps
    submitted_at      TIMESTAMPTZ,
    reviewed_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id)  -- un tenant, una solicitud activa
);

ALTER TABLE onboarding_applications ENABLE ROW LEVEL SECURITY;
CREATE POLICY onboarding_tenant_isolation ON onboarding_applications
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

CREATE TABLE onboarding_documents (
    id              UUID        PRIMARY KEY,
    application_id  UUID        NOT NULL REFERENCES onboarding_applications(id),
    tenant_id       UUID        NOT NULL,
    document_type   TEXT        NOT NULL,
    reference       TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending','verified','rejected')),
    notes           TEXT,
    uploaded_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_onboarding_docs_app ON onboarding_documents (application_id);

ALTER TABLE onboarding_documents ENABLE ROW LEVEL SECURITY;
CREATE POLICY onboarding_docs_isolation ON onboarding_documents
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

CREATE TABLE onboarding_persons (
    id                  UUID        PRIMARY KEY,
    application_id      UUID        NOT NULL REFERENCES onboarding_applications(id),
    tenant_id           UUID        NOT NULL,
    full_name           TEXT        NOT NULL,
    role                TEXT        NOT NULL
                                    CHECK (role IN ('owner','director','ubo')),
    identity_doc_type   TEXT,
    identity_doc_number TEXT,
    nationality         TEXT,
    created_at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_onboarding_persons_app ON onboarding_persons (application_id);

ALTER TABLE onboarding_persons ENABLE ROW LEVEL SECURITY;
CREATE POLICY onboarding_persons_isolation ON onboarding_persons
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

CREATE TABLE onboarding_bank_accounts (
    id             UUID        PRIMARY KEY,
    application_id UUID        NOT NULL REFERENCES onboarding_applications(id),
    tenant_id      UUID        NOT NULL,
    account_type   TEXT        NOT NULL CHECK (account_type IN ('CBU','CVU','IBAN')),
    account_number TEXT        NOT NULL,
    bank_name      TEXT,
    holder_name    TEXT        NOT NULL,
    currency       CHAR(3)     NOT NULL,
    verified       BOOLEAN     NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL,
    UNIQUE (application_id)   -- una cuenta bancaria por solicitud
);

ALTER TABLE onboarding_bank_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY onboarding_bank_isolation ON onboarding_bank_accounts
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE onboarding_applications IS 'Solicitudes de KYC/KYB. Una por tenant.';
COMMENT ON TABLE onboarding_documents    IS 'Documentos cargados para validación.';
COMMENT ON TABLE onboarding_persons      IS 'Titulares, directores y UBOs del negocio.';
COMMENT ON TABLE onboarding_bank_accounts IS 'Cuenta bancaria para desembolsos.';
