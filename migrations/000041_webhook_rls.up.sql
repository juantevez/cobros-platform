-- migrations/000041_webhook_rls.up.sql
-- RLS efectiva en las tablas de Webhook, como en el resto del sistema.
--   - Aislamiento por tenant tolerante a variable vacía (NULLIF: tras una tx
--     con set_config(..., is_local=true) la conexión conserva '' y ''::uuid
--     falla con 22P02).
--   - Operador de plataforma solo de lectura: el RetryPoller lista las
--     deliveries vencidas de todos los tenants; cada despacho corre después
--     con el tenant de su delivery.
--   - webhook_delivery_attempts pasa a tener tenant_id y RLS: antes cualquier
--     sesión leía los intentos (y las respuestas del comercio) de cualquiera.
-- Los repositorios, el consumer y el poller fijan app.current_tenant.

DROP POLICY webhook_endpoints_isolation ON webhook_endpoints;
CREATE POLICY webhook_endpoints_tenant_isolation ON webhook_endpoints
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY webhook_endpoints_platform_operator ON webhook_endpoints FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');

DROP POLICY webhook_deliveries_isolation ON webhook_deliveries;
CREATE POLICY webhook_deliveries_tenant_isolation ON webhook_deliveries
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY webhook_deliveries_platform_operator ON webhook_deliveries FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');

ALTER TABLE webhook_delivery_attempts ADD COLUMN tenant_id UUID;

UPDATE webhook_delivery_attempts a
SET tenant_id = d.tenant_id
FROM webhook_deliveries d
WHERE d.id = a.delivery_id;

ALTER TABLE webhook_delivery_attempts ALTER COLUMN tenant_id SET NOT NULL;

-- El intento es del mismo tenant que su delivery: FK compuesta.
CREATE UNIQUE INDEX uq_webhook_deliveries_id_tenant ON webhook_deliveries (id, tenant_id);
ALTER TABLE webhook_delivery_attempts DROP CONSTRAINT webhook_delivery_attempts_delivery_id_fkey;
ALTER TABLE webhook_delivery_attempts
    ADD CONSTRAINT webhook_delivery_attempts_delivery_tenant_fkey
    FOREIGN KEY (delivery_id, tenant_id) REFERENCES webhook_deliveries (id, tenant_id);

ALTER TABLE webhook_delivery_attempts ENABLE ROW LEVEL SECURITY;
CREATE POLICY webhook_delivery_attempts_tenant_isolation ON webhook_delivery_attempts
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid);
CREATE POLICY webhook_delivery_attempts_platform_operator ON webhook_delivery_attempts FOR SELECT
    USING (current_setting('app.platform_operator', true) = 'on');
