-- migrations/000002_auth_init.up.sql
-- Tablas del contexto Auth & Multi-Tenant.
--
-- Estrategia de aislamiento:
--   - tenants: raíz del sistema, sin tenant_id ni RLS.
--   - users, memberships: RLS habilitado; el TxManager fija app.current_tenant.
--   - api_keys: sin RLS (se busca por prefix global); aislamiento en código.
--   - refresh_tokens: sin RLS (se busca por hash global); son credenciales de sesión.

-- CITEXT permite comparaciones case-insensitive de email sin normalizar en app.
CREATE EXTENSION IF NOT EXISTS citext;

-- ── Tenants ───────────────────────────────────────────────────────────────────

CREATE TABLE tenants (
    id          UUID        PRIMARY KEY,
    legal_name  TEXT        NOT NULL,
    status      TEXT        NOT NULL
                            CHECK (status IN ('pending', 'active', 'suspended')),
    environment TEXT        NOT NULL
                            CHECK (environment IN ('test', 'production')),
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

COMMENT ON TABLE tenants IS 'Comercios registrados en la plataforma (raíz del multi-tenant).';

-- ── Users ─────────────────────────────────────────────────────────────────────

CREATE TABLE users (
    id            UUID        PRIMARY KEY,
    tenant_id     UUID        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    email         CITEXT      NOT NULL,
    password_hash TEXT        NOT NULL,
    status        TEXT        NOT NULL
                              CHECK (status IN ('active', 'suspended')),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    -- Email único dentro del mismo tenant; CITEXT hace la comparación case-insensitive.
    UNIQUE (tenant_id, email)
);

CREATE INDEX idx_users_tenant_email ON users (tenant_id, email);

-- RLS: cada fila es visible solo si tenant_id coincide con la variable de sesión.
-- El TxManager ejecuta: SELECT set_config('app.current_tenant', $1, true)
-- current_setting con true (is_local): si la var no está, retorna NULL sin error.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;

CREATE POLICY users_tenant_isolation ON users
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE users IS 'Usuarios con acceso a la plataforma; aislados por tenant vía RLS.';

-- ── Memberships ───────────────────────────────────────────────────────────────

CREATE TABLE memberships (
    user_id     UUID        NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role        TEXT        NOT NULL
                            CHECK (role IN ('admin', 'operator', 'accountant',
                                            'read_only', 'platform_support')),
    assigned_by UUID        REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, tenant_id)
);

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;

CREATE POLICY memberships_tenant_isolation ON memberships
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

COMMENT ON TABLE memberships IS 'Vínculo usuario-tenant con rol (RBAC). Un user puede estar en N tenants.';

-- ── API Keys ──────────────────────────────────────────────────────────────────

CREATE TABLE api_keys (
    id          UUID        PRIMARY KEY,
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    -- prefix: 8 chars del secreto, visible para identificación.
    -- UNIQUE globalmente para que FindByPrefix funcione sin tenant context.
    prefix      TEXT        NOT NULL UNIQUE,
    key_hash    TEXT        NOT NULL,
    environment TEXT        NOT NULL
                            CHECK (environment IN ('test', 'production')),
    scopes      TEXT[]      NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_api_keys_tenant ON api_keys (tenant_id)
    WHERE revoked_at IS NULL;

-- Sin RLS: el aislamiento se garantiza en código comparando apiKey.TenantID()
-- con el tenant del request. FindByPrefix necesita buscar sin tenant context.
COMMENT ON TABLE api_keys IS 'Credenciales server-to-server de cada tenant.';

-- ── Refresh Tokens ────────────────────────────────────────────────────────────

CREATE TABLE refresh_tokens (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id   UUID        NOT NULL,
    -- token_hash: hash argon2id del secreto en claro. UNIQUE para buscar por hash.
    token_hash  TEXT        NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    -- replaced_by: referencia al token sucesor (rotación).
    replaced_by UUID        REFERENCES refresh_tokens(id)
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id)
    WHERE revoked_at IS NULL;

-- Sin RLS: la búsqueda es por hash del token, que es naturalmente único y secreto.
COMMENT ON TABLE refresh_tokens IS 'Tokens de renovación de sesión con rotación.';
