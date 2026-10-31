-- migrations/000043_dispute_rls.up.sql
-- RLS efectiva en Dispute, como en el resto del sistema.
--   - Aislamiento por tenant tolerante a variable vacía (NULLIF: tras una tx
--     con set_config(..., is_local=true) la conexión conserva '' y ''::uuid
--     falla con 22P02).
--   - Operador de plataforma solo de lectura: abre y resuelve disputas de
--     cualquier tenant y el ExpiryPoller lista las vencidas de todos. Las
--     escrituras corren con el tenant de la disputa.
--   - dispute_evidence pasa a tener tenant_id y RLS.
--   - payments: el operador que abre una disputa lee el pago de otro tenant.
--     La política de tenant gana NULLIF por la misma razón de arriba: sin él,
--     una conexión con '' falla aunque la de operador pase.

DROP POLICY disputes_tenant_isolation ON disputes;
CREATE POLICY disputes_tenant_isolation ON disputes
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY disputes_platform_operator ON disputes FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');

ALTER TABLE dispute_evidence ADD COLUMN tenant_id UUID;

UPDATE dispute_evidence e
SET tenant_id = d.tenant_id
FROM disputes d
WHERE d.id = e.dispute_id;

ALTER TABLE dispute_evidence ALTER COLUMN tenant_id SET NOT NULL;

-- La evidencia es del mismo tenant que su disputa: FK compuesta.
CREATE UNIQUE INDEX uq_disputes_id_tenant ON disputes (id, tenant_id);
ALTER TABLE dispute_evidence DROP CONSTRAINT dispute_evidence_dispute_id_fkey;
ALTER TABLE dispute_evidence
    ADD CONSTRAINT dispute_evidence_dispute_tenant_fkey
    FOREIGN KEY (dispute_id, tenant_id) REFERENCES disputes (id, tenant_id);

ALTER TABLE dispute_evidence ENABLE ROW LEVEL SECURITY;
CREATE POLICY dispute_evidence_tenant_isolation ON dispute_evidence
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY dispute_evidence_platform_operator ON dispute_evidence FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');

DROP POLICY payments_tenant_isolation ON payments;
CREATE POLICY payments_tenant_isolation ON payments
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY payments_platform_operator ON payments FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');
