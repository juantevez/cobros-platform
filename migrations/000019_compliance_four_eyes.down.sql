-- migrations/000019_compliance_four_eyes.down.sql
-- Las alertas pendientes de segunda aprobación vuelven a open (se pierde la
-- propuesta). Los usuarios compliance_officer deben reasignarse antes del down.

DROP POLICY IF EXISTS aml_alerts_platform_operator ON aml_alerts;

DROP INDEX IF EXISTS idx_aml_alerts_review_queue;
ALTER TABLE aml_alerts DROP CONSTRAINT IF EXISTS aml_alerts_pending_has_proposal;
UPDATE aml_alerts SET status = 'open' WHERE status = 'pending_second_approval';
ALTER TABLE aml_alerts
    DROP COLUMN IF EXISTS proposed_status,
    DROP COLUMN IF EXISTS proposed_by,
    DROP COLUMN IF EXISTS proposed_at,
    DROP COLUMN IF EXISTS approval_note;

ALTER TABLE aml_alerts DROP CONSTRAINT aml_alerts_status_check;
ALTER TABLE aml_alerts ADD CONSTRAINT aml_alerts_status_check
    CHECK (status IN ('open', 'cleared', 'confirmed'));

ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('admin', 'operator', 'accountant', 'read_only', 'platform_support'));
