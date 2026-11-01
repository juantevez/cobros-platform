-- migrations/000050_auth_rls.up.sql
-- RLS efectiva en users y memberships, como en el resto del sistema:
-- aislamiento por tenant tolerante a variable vacía (NULLIF: tras una tx con
-- set_config(..., is_local=true) la conexión conserva '' y ''::uuid falla
-- con 22P02). Los repos de Auth fijan app.current_tenant en cada método.
-- api_keys y refresh_tokens siguen sin RLS: se buscan por prefix y por hash,
-- antes de conocer el tenant.
DROP POLICY users_tenant_isolation ON users;
CREATE POLICY users_tenant_isolation ON users
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY memberships_tenant_isolation ON memberships;
CREATE POLICY memberships_tenant_isolation ON memberships
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
