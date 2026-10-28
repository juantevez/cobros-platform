-- migrations/000019_compliance_four_eyes.up.sql
-- Compliance & AML — revisión por la plataforma con four-eyes.
-- Ver docs/technical/COMPLIANCE.md.
--
--   1. Rol de plataforma compliance_officer (no asignable por el tenant; se
--      valida en código con Role.IsTenantAssignable).
--   2. aml_alerts: estado pending_second_approval + columnas de la propuesta
--      del primer analista y la nota del segundo.
--   3. aml_alerts: política RLS de operador de plataforma. La revisión es
--      cross-tenant; la app fija app.platform_operator='on' solo detrás del
--      guard de rol (pkg/postgres.WithPlatformOperator).

-- 1 ─────────────────────────────────────────────────────────────────────────
ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('admin', 'operator', 'accountant', 'read_only',
                    'platform_support', 'compliance_officer'));

-- 2 ─────────────────────────────────────────────────────────────────────────
ALTER TABLE aml_alerts DROP CONSTRAINT aml_alerts_status_check;
ALTER TABLE aml_alerts ADD CONSTRAINT aml_alerts_status_check
    CHECK (status IN ('open', 'pending_second_approval', 'cleared', 'confirmed'));

ALTER TABLE aml_alerts
    ADD COLUMN proposed_status TEXT CHECK (proposed_status IN ('cleared', 'confirmed')),
    ADD COLUMN proposed_by     TEXT,
    ADD COLUMN proposed_at     TIMESTAMPTZ,
    ADD COLUMN approval_note   TEXT;

-- Una alerta pendiente siempre tiene propuesta completa.
ALTER TABLE aml_alerts ADD CONSTRAINT aml_alerts_pending_has_proposal
    CHECK (status <> 'pending_second_approval'
           OR (proposed_status IS NOT NULL AND proposed_by IS NOT NULL AND proposed_at IS NOT NULL));

-- Bandeja de cumplimiento: alertas por estado de toda la plataforma.
CREATE INDEX idx_aml_alerts_review_queue ON aml_alerts (status, created_at DESC);

-- 3 ─────────────────────────────────────────────────────────────────────────
CREATE POLICY aml_alerts_platform_operator ON aml_alerts
    USING (current_setting('app.platform_operator', true) = 'on');
