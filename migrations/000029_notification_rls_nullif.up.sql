-- migrations/000029_notification_rls_nullif.up.sql
-- Políticas RLS de Notification tolerantes a variable vacía: tras una tx que
-- usó set_config(..., is_local=true), la conexión del pool conserva
-- app.current_tenant = '' (no NULL) y ''::uuid falla con 22P02. NULLIF la
-- convierte en NULL → la política evalúa false (0 filas, sin error). Los
-- repositorios fijan la variable en cada operación (RunWithAppTenant).

DROP POLICY notifications_isolation ON notifications;
CREATE POLICY notifications_isolation ON notifications
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);

DROP POLICY notification_prefs_isolation ON notification_preferences;
CREATE POLICY notification_prefs_isolation ON notification_preferences
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
