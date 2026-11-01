DROP POLICY users_tenant_isolation ON users;
CREATE POLICY users_tenant_isolation ON users
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY memberships_tenant_isolation ON memberships;
CREATE POLICY memberships_tenant_isolation ON memberships
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
