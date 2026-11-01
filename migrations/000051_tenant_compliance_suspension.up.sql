-- migrations/000051_tenant_compliance_suspension.up.sql
-- Marca si la suspensión vigente es por cumplimiento: esa solo la levanta un
-- compliance_officer; el resto, platform_support. El motivo que viaja en
-- auth.tenant.suspended.v1 es genérico (anti tipping-off), así que no sirve
-- para distinguirlas.
ALTER TABLE tenants ADD COLUMN suspended_for_compliance BOOLEAN NOT NULL DEFAULT false;

-- Suspensiones previas de tenants con un hold de cumplimiento (vigente o
-- liberado): se tratan como de cumplimiento, del lado seguro.
UPDATE tenants t SET suspended_for_compliance = true
WHERE t.status = 'suspended'
  AND EXISTS (SELECT 1 FROM tenant_compliance_holds h WHERE h.tenant_id = t.id);
