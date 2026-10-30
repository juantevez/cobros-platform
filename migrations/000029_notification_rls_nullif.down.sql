-- migrations/000029_notification_rls_nullif.down.sql
DROP POLICY notifications_isolation ON notifications;
CREATE POLICY notifications_isolation ON notifications
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);

DROP POLICY notification_prefs_isolation ON notification_preferences;
CREATE POLICY notification_prefs_isolation ON notification_preferences
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
